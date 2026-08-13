package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Config struct {
	Type     string
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	FilePath string
}

func Connect(cfg Config) (*sql.DB, error) {
	var driverName string
	var dataSourceName string

	switch cfg.Type {
	case "postgres":
		driverName = "pgx"
		dataSourceName = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable&default_query_exec_mode=simple_protocol",
			cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)
	case "mysql", "mariadb":
		// Refused rather than connected. The DDL layer is multi-vendor — the
		// migrations swap types per engine — but the DML layer is not: the
		// embedded queries use PostgreSQL's $1 positional parameters, which
		// MySQL and MariaDB do not accept, so every query fails at runtime.
		//
		// Connecting anyway produces the worst version of that: setup
		// succeeds, the schema is created, and the failure only appears later
		// when someone tries to use it. Refusing here says so at the one
		// moment it can still be acted on.
		return nil, fmt.Errorf(
			"database type %q is not supported: the query layer uses PostgreSQL-style "+
				"positional parameters, so %s connects but every query fails. Use postgres or sqlite",
			cfg.Type, cfg.Type)
	case "sqlite":
		driverName = "sqlite"
		dataSourceName = sqliteDSN(cfg.FilePath)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	if cfg.Type == "sqlite" {
		tuneSQLitePool(db)
	} else {
		db.SetMaxOpenConns(100)
		db.SetMaxIdleConns(25)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

type Connection interface {
	GetDB() *sql.DB
	SetDB(db *sql.DB)
	IsConnected() bool
}

type connection struct {
	db *sql.DB
}

func NewConnection(db *sql.DB) Connection {
	return &connection{db: db}
}

func (c *connection) GetDB() *sql.DB {
	return c.db
}

func (c *connection) SetDB(db *sql.DB) {
	c.db = db
}

func (c *connection) IsConnected() bool {
	return c.db != nil
}

// sqliteDSN adds the pragmas a SQLite database needs to survive concurrent use.
//
// Opening the file path on its own — which is what this did — leaves
// busy_timeout at zero and the journal in rollback mode. A gateway runs four
// background workers alongside its request handlers, all writing, so with those
// defaults the second writer to arrive does not wait its turn: it fails
// immediately with SQLITE_BUSY. In practice that surfaced as
//
//	failed to claim pending outbox emails: database is locked (5) (SQLITE_BUSY)
//
// which means queued mail simply stops being sent for that tick.
//
//   - busy_timeout makes a blocked writer wait rather than fail. Five seconds is
//     far longer than any statement here takes and far shorter than the outbox
//     poll interval.
//   - WAL lets readers continue while a write is in progress. Under the default
//     rollback journal a single writer blocks every reader, so the dashboard
//     stalls whenever the queue drains.
//   - foreign_keys is off by default in SQLite, which silently permits rows that
//     reference a tenant that does not exist.
//
// The test harness has always set these, which is why nothing caught it: the
// tests were configured more carefully than the code they exercise.
func sqliteDSN(path string) string {
	if strings.Contains(path, "?") {
		// The operator has supplied their own parameters; respect them rather
		// than producing a malformed DSN with two query strings.
		return path
	}
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
}

// tuneSQLitePool sizes the pool for an engine that serialises writes.
//
// SQLite permits one writer at a time whatever the pool says, so a hundred
// connections do not buy throughput — they buy a hundred goroutines contending
// for one lock and burning the busy_timeout. A small pool keeps the contention
// in Go's queue, where it is cheap and fair, instead of in the driver.
func tuneSQLitePool(db *sql.DB) {
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxOpenConns)
}

// Chosen to be greater than one so reads still overlap under WAL, and small
// enough that writers queue in Go rather than in the driver.
const sqliteMaxOpenConns = 4
