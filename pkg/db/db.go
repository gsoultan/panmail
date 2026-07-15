package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
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
		driverName = "mysql"
		dataSourceName = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
			cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)
	case "sqlite":
		driverName = "sqlite"
		dataSourceName = cfg.FilePath
	default:
		return nil, fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}

	// Optimize connection pooling
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(25)
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
