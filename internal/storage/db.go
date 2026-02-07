package storage

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var migration001 string

// DB wraps a SQLite connection with metrics-specific operations.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex // serialize writes
}

// Open creates or opens the SQLite database.
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "cudascope.db")
	conn, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Single writer connection for SQLite
	conn.SetMaxOpenConns(1)

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Printf("database opened at %s", dbPath)
	return db, nil
}

func (db *DB) migrate() error {
	// Check current version
	var version int
	err := db.conn.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version)
	if err != nil {
		// Table doesn't exist yet, run initial migration
		version = 0
	}

	if version < 1 {
		if _, err := db.conn.Exec(migration001); err != nil {
			return fmt.Errorf("migration 001: %w", err)
		}
		log.Println("applied migration 001")
	}

	return nil
}

// Close closes the database.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB for advanced queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}
