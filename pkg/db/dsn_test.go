package db

import (
	"net/url"
	"strings"
	"testing"
)

// The DSN was built with Sprintf and the password dropped in unescaped. That
// holds until someone uses a password a generator produced.
func TestAPasswordWithAnAtSignStillReachesTheRightHost(t *testing.T) {
	cfg := Config{
		Type: "postgres", Host: "db.internal", Port: 5432,
		User: "panmail", Password: "s3cr3t@#/?:pass", DBName: "panmail",
	}

	u, err := url.Parse(postgresDSN(cfg))
	if err != nil {
		t.Fatalf("the DSN does not parse: %v", err)
	}

	// The failure this exists for: "@" ends the userinfo, so everything after
	// it becomes the host. On a machine where that name resolves, the gateway
	// connects to a server nobody configured.
	if u.Hostname() != cfg.Host {
		t.Errorf("host = %q, want %q: the password broke out of the userinfo", u.Hostname(), cfg.Host)
	}
	if u.Port() != "5432" {
		t.Errorf("port = %q, want 5432", u.Port())
	}
	if got, _ := u.User.Password(); got != cfg.Password {
		t.Errorf("password round-tripped as %q, want %q", got, cfg.Password)
	}
	if u.User.Username() != cfg.User {
		t.Errorf("user = %q, want %q", u.User.Username(), cfg.User)
	}
	if strings.TrimPrefix(u.Path, "/") != cfg.DBName {
		t.Errorf("database = %q, want %q", u.Path, cfg.DBName)
	}
}

// Everything panmail stores crosses this socket. Refusing to encrypt it was
// not a setting, it was the only behaviour.
func TestTheConnectionCanBeEncrypted(t *testing.T) {
	for _, mode := range []string{"require", "verify-ca", "verify-full", "disable"} {
		cfg := Config{Type: "postgres", Host: "db.internal", Port: 5432, DBName: "panmail", SSLMode: mode}

		u, err := url.Parse(postgresDSN(cfg))
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if got := u.Query().Get("sslmode"); got != mode {
			t.Errorf("sslmode = %q, want %q", got, mode)
		}
	}
}

// The default has to be the strongest one that cannot break a deployment that
// already works. "prefer" uses TLS wherever the server offers it — which every
// managed PostgreSQL does — and still connects to one that does not.
func TestTheDefaultIsNotPlaintext(t *testing.T) {
	if defaultSSLMode == "disable" {
		t.Fatal("the default refuses TLS outright")
	}

	cfg := Config{Type: "postgres", Host: "db.internal", Port: 5432, DBName: "panmail"}
	u, _ := url.Parse(postgresDSN(cfg))
	if got := u.Query().Get("sslmode"); got != defaultSSLMode {
		t.Errorf("unset ssl_mode produced %q, want %q", got, defaultSSLMode)
	}
}

// PgBouncer in transaction mode cannot serve server-side prepared statements,
// which is what pgx's default exec mode uses.
func TestThePoolerCompatibleExecModeSurvivedTheRewrite(t *testing.T) {
	u, _ := url.Parse(postgresDSN(Config{Type: "postgres", Host: "h", Port: 5432, DBName: "d"}))
	if got := u.Query().Get("default_query_exec_mode"); got != "simple_protocol" {
		t.Errorf("default_query_exec_mode = %q, want simple_protocol", got)
	}
}

// A warning about an unencrypted connection to 127.0.0.1 is noise, and noise
// in a security warning is how the real one gets ignored.
func TestLoopbackIsNotWarnedAbout(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if !isLoopback(host) {
			t.Errorf("%s is not recognised as loopback", host)
		}
	}
	for _, host := range []string{"db.internal", "10.0.0.5", "example.com"} {
		if isLoopback(host) {
			t.Errorf("%s treated as loopback; a plaintext connection to it would go unreported", host)
		}
	}
}
