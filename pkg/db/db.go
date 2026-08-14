package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
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

	// MaxOpenConns bounds this process's share of the server's connections.
	// Zero takes defaultMaxOpenConns. The other fields are matched by their
	// lowercased names; this one is two words, so it says so.
	MaxOpenConns int `yaml:"max_open_conns"`

	// SSLMode is passed to PostgreSQL verbatim: disable, allow, prefer,
	// require, verify-ca or verify-full. Empty takes defaultSSLMode.
	SSLMode string `yaml:"ssl_mode"`
}

// prefer, because it is the strongest default that cannot break a deployment
// that already works: a server offering TLS gets it, one that does not still
// connects. See sslMode for why that is a floor rather than a destination.
const defaultSSLMode = "prefer"

// A gateway is stateless so that several can be run against one database, and
// this is the number that decides whether that actually works. The pool used to
// be pinned at 100 — PostgreSQL's own default max_connections — so a single
// instance was sized to consume every slot the server had. A second instance
// then spent its life losing races for connections, and the way that surfaced
// was not a clean error: a send would succeed, the delete of the outbox row
// would fail with "sorry, too many clients already", the row would stay
// PENDING, and the message would be sent again. Exhausting the connection
// budget delivered duplicate mail.
//
// 25 leaves room for three instances plus migrations, an interactive psql and
// the superuser reservation inside a default server. Operators running more
// instances, or a server tuned higher, set database.max_open_conns; the
// arithmetic they need is
//
//	instances × max_open_conns + headroom ≤ server max_connections
const defaultMaxOpenConns = 25

func Connect(cfg Config) (*sql.DB, error) {
	var driverName string
	var dataSourceName string

	switch cfg.Type {
	case "postgres":
		driverName = "pgx"
		dataSourceName = postgresDSN(cfg)
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
		maxOpen := cfg.MaxOpenConns
		if maxOpen <= 0 {
			maxOpen = defaultMaxOpenConns
		}
		db.SetMaxOpenConns(maxOpen)
		// Idle tracks open: holding fewer idle than the pool will open means
		// the connections above that mark are closed and reopened on every
		// burst, which is the load pattern here — a queue tick claims a batch
		// and goes quiet.
		db.SetMaxIdleConns(maxOpen)
	}
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	if cfg.Type == "postgres" {
		warnIfInTheClear(db, cfg)
	}

	return db, nil
}

// postgresDSN builds the connection URL.
//
// This was a Sprintf with the password interpolated straight into the string,
// which works right up until someone uses a generated password. A password
// containing "@" ends the userinfo early and the rest of it becomes the host,
// so the connection either fails with an error naming a host nobody configured
// or, on a machine where that name resolves, succeeds against the wrong server.
// url.URL escapes each part for what it is.
func postgresDSN(cfg Config) string {
	q := url.Values{}
	q.Set("sslmode", sslMode(cfg))
	// The pgx extended protocol prepares statements server-side, which
	// transaction-mode poolers such as PgBouncer do not support.
	q.Set("default_query_exec_mode", "simple_protocol")

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:     "/" + cfg.DBName,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// sslMode resolves what the connection should negotiate.
//
// It used to be "disable", hard-coded, so there was no way to encrypt the
// database connection at all. Everything panmail stores crosses that socket:
// message bodies, recipient addresses, API keys and session material as they
// are read back. Stored credentials are encrypted at rest under their own key,
// which is worth having and does nothing for the wire.
//
// The default is "prefer" rather than "require" because it cannot break a
// working deployment — a server without TLS still connects — while every
// managed PostgreSQL offers TLS and will now be used over it. That is a real
// improvement and an incomplete one: "prefer" falls back silently and verifies
// nothing, so anyone crossing a network they do not own wants "verify-full".
// warnIfInTheClear says so when it applies.
func sslMode(cfg Config) string {
	if cfg.SSLMode != "" {
		return cfg.SSLMode
	}
	return defaultSSLMode
}

// warnIfInTheClear reports a connection that ended up unencrypted to somewhere
// other than this machine.
//
// "prefer" is silent about falling back, which is what makes it safe to default
// to and also what makes it easy to believe a connection is protected when it
// is not. Asking the server settles it.
func warnIfInTheClear(db *sql.DB, cfg Config) {
	if isLoopback(cfg.Host) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var encrypted bool
	// pg_stat_ssl describes this backend, so this is the connection actually
	// in use rather than what was requested.
	err := db.QueryRowContext(ctx,
		"SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&encrypted)
	if err != nil {
		// Not worth failing a startup over: a pooler may not expose it.
		slog.Debug("could not determine whether the database connection is encrypted", "error", err)
		return
	}
	if encrypted {
		return
	}

	slog.Warn("the database connection is not encrypted",
		"host", cfg.Host, "sslmode", sslMode(cfg),
		"detail", "every query crosses the network in the clear, including message bodies and recipient addresses",
		"fix", "set database.ssl_mode to verify-full, or require if the server has no certificate you can verify")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
