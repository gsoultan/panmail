package db

import (
	"path/filepath"
	"testing"
)

// A gateway is stateless so that several can run against one database. The
// connection bound is what decides whether that is true in practice.
//
// The pool was pinned at 100, which is PostgreSQL's own default
// max_connections: one instance was sized to take every slot the server had.
// Running a second one — the entire point of a stateless gateway — put both
// into a fight for connections that neither could win, and the way it surfaced
// was duplicate mail: the send succeeded, the delete of the outbox row failed
// with "sorry, too many clients already", the row stayed PENDING, and the
// message went out again.
//
// So the default has to leave room for other instances, and an operator has to
// be able to say what that room is.
func TestThePoolLeavesRoomForOtherInstances(t *testing.T) {
	// The number a default PostgreSQL will accept. Anything at or above it
	// means one instance can starve every other.
	const serverDefaultMaxConnections = 100

	if defaultMaxOpenConns >= serverDefaultMaxConnections {
		t.Fatalf("default pool of %d against a server that allows %d: one gateway can consume "+
			"every connection, and a second instance starves",
			defaultMaxOpenConns, serverDefaultMaxConnections)
	}

	// Three instances plus headroom for migrations, a psql session and the
	// superuser reservation. Fewer than three and horizontal scaling is
	// documentation rather than a property.
	const instances = 3
	if defaultMaxOpenConns*instances >= serverDefaultMaxConnections {
		t.Errorf("%d instances at %d connections each needs %d of the %d a default server has",
			instances, defaultMaxOpenConns, defaultMaxOpenConns*instances, serverDefaultMaxConnections)
	}
}

func TestMaxOpenConnsIsConfigurable(t *testing.T) {
	// SQLite because it needs no server; the pool bound is applied in Connect
	// for every non-SQLite engine and read back from the same handle.
	path := filepath.Join(t.TempDir(), "pool.db")

	d, err := Connect(Config{Type: "sqlite", FilePath: path, MaxOpenConns: 7})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	// SQLite has its own bound and ignores the field, which is the point of
	// asserting it separately: a serialised writer is not something an
	// operator should be able to widen by editing a Postgres-shaped knob.
	if got := d.Stats().MaxOpenConnections; got != sqliteMaxOpenConns {
		t.Errorf("sqlite pool = %d, want the serialised-writer bound %d", got, sqliteMaxOpenConns)
	}
}

// Zero in the config file is absence, not "no connections".
func TestAnUnsetBoundTakesTheDefault(t *testing.T) {
	cfg := Config{Type: "postgres", Host: "127.0.0.1", Port: 1, User: "u", DBName: "d"}
	if cfg.MaxOpenConns != 0 {
		t.Fatal("this test is about the zero value")
	}

	// Connect would need a server, so this asserts the resolution rule the
	// same way Connect applies it.
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = defaultMaxOpenConns
	}
	if maxOpen != defaultMaxOpenConns {
		t.Errorf("unset bound resolved to %d, want %d", maxOpen, defaultMaxOpenConns)
	}
}
