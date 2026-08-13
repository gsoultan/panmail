package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// Connecting to MySQL or MariaDB succeeded and then failed on every query. The
// DDL layer swaps types per engine, so setup completed and created a schema,
// and the breakage only appeared later when someone tried to use it — the
// embedded queries use PostgreSQL's $1 positional parameters, which those
// engines do not accept.
//
// Refusing at the one moment it can still be acted on is the whole point.
func TestMySQLIsRefusedRatherThanHalfSupported(t *testing.T) {
	for _, engine := range []string{"mysql", "mariadb"} {
		_, err := Connect(Config{Type: engine, Host: "localhost", Port: 3306, DBName: "panmail"})
		if err == nil {
			t.Errorf("%s connected; setup would succeed and every query would then fail", engine)
			continue
		}
		// The message has to say what to use instead, or an operator sees a
		// refusal with no route forward.
		if !strings.Contains(err.Error(), "postgres") || !strings.Contains(err.Error(), "sqlite") {
			t.Errorf("%s: error %q does not say what is supported", engine, err)
		}
	}
}

func TestAnUnknownEngineIsRefused(t *testing.T) {
	if _, err := Connect(Config{Type: "oracle"}); err == nil {
		t.Error("an unknown engine was accepted")
	}
}

func TestSupportedEnginesAreStillAccepted(t *testing.T) {
	// SQLite proves the accepted path is untouched; postgres needs a server.
	d, err := Connect(Config{Type: "sqlite", FilePath: filepath.Join(t.TempDir(), "ok.db")})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	_ = d.Close()
}
