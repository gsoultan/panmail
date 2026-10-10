package db

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"
)

// closedPort returns a loopback port nothing is listening on, so a connection
// to it is refused at once.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// settledGoroutines reads the goroutine count once it has stopped falling, so
// goroutines that are already on their way out are not counted as leaked.
func settledGoroutines() int {
	n := runtime.NumGoroutine()
	for range 50 {
		time.Sleep(10 * time.Millisecond)
		m := runtime.NumGoroutine()
		if m >= n {
			return m
		}
		n = m
	}
	return n
}

// A gateway retrying a database that is down calls Connect every few seconds.
// Each failed ping used to return without closing the handle sql.Open had
// made, and every handle keeps a goroutine for as long as it lives: fifty
// failures left fifty goroutines and fifty pools behind, for the life of the
// process.
func TestAFailedConnectLeavesNothingBehind(t *testing.T) {
	cfg := Config{Type: "postgres", Host: "127.0.0.1", Port: closedPort(t), User: "u", DBName: "d", SSLMode: "disable"}

	before := settledGoroutines()

	const attempts = 30
	for range attempts {
		if d, err := Connect(cfg); err == nil {
			_ = d.Close()
			t.Fatal("connected to a port nothing is listening on")
		}
	}

	// A little slack for runtime goroutines that come and go; a leak is one
	// per attempt and cannot hide in it.
	if after := settledGoroutines(); after-before >= attempts/2 {
		t.Errorf("goroutines went from %d to %d over %d failed connects; each failure is leaking its pool",
			before, after, attempts)
	}
}

// pgx sets no connect timeout. Against a host that accepts the connection and
// then says nothing — or a firewall that drops packets — a ping with no
// deadline waits on the network for minutes, and a gateway booting against it
// sits there saying nothing. This server accepts and never answers.
func TestConnectGivesUpOnASilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	accepted := make(chan net.Conn)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	restore := pingTimeout
	pingTimeout = 200 * time.Millisecond
	defer func() { pingTimeout = restore }()

	cfg := Config{Type: "postgres", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, User: "u", DBName: "d", SSLMode: "disable"}

	done := make(chan error, 1)
	go func() {
		d, err := Connect(cfg)
		if d != nil {
			_ = d.Close()
		}
		done <- err
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-accepted:
			held = append(held, c)
		case err := <-done:
			if err == nil {
				t.Fatal("connected to a server that never answered")
			}
			return
		case <-deadline:
			t.Fatal("Connect was still waiting on a silent server after 5s; the ping has no deadline")
		}
	}
}

// A configuration mistake is reported as one, so a caller retrying an
// unreachable database stops at once rather than spending its budget on an
// engine that will never be supported. An unreachable server is not one.
func TestConfigurationMistakesAreTellable(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		invalid bool
	}{
		{"mysql", Config{Type: "mysql"}, true},
		{"unknown engine", Config{Type: "oracle"}, true},
		{"sqlite without a path", Config{Type: "sqlite"}, true},
		{"unreachable postgres", Config{Type: "postgres", Host: "127.0.0.1", Port: closedPort(t), SSLMode: "disable"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Connect(tc.cfg)
			if err == nil {
				_ = d.Close()
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, ErrInvalidConfig); got != tc.invalid {
				t.Errorf("errors.Is(%v, ErrInvalidConfig) = %v, want %v", err, got, tc.invalid)
			}
		})
	}
}
