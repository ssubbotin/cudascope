package storage

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var migration001 string

//go:embed migrations/002_host_rollup.sql
var migration002 string

//go:embed migrations/003_multinode.sql
var migration003 string

//go:embed migrations/004_rollup_unique.sql
var migration004 string

//go:embed migrations/005_vllm.sql
var migration005 string

//go:embed migrations/006_alert_events.sql
var migration006 string

//go:embed migrations/007_vllm_rollup.sql
var migration007 string

//go:embed migrations/008_raw_unique.sql
var migration008 string

//go:embed migrations/009_throttle_ecc.sql
var migration009 string

//go:embed migrations/010_rollup_throttle.sql
var migration010 string

//go:embed migrations/011_alert_power_limit.sql
var migration011 string

//go:embed migrations/012_vllm_nullable_ratios.sql
var migration012 string

// Options tunes the queries whose answer depends on how often this
// deployment collects.
type Options struct {
	// FreshWindow is how old the newest sample may be and still count as
	// current. It follows the collection interval, because a hardcoded
	// window silently empties the dashboard for anyone who collects more
	// slowly than the author assumed.
	FreshWindow time.Duration

	// NodeOfflineAfter is the heartbeat age at which a node stops counting
	// as online. The alert engine reads the same setting, so the dot in the
	// node list and the node_silent event agree by construction.
	NodeOfflineAfter time.Duration

	// RawRetention and M1Retention are how long those tiers survive. History
	// queries need them: a window narrow enough for raw resolution but older
	// than raw retention has to be answered from the rollup, and reading the
	// empty raw table instead drew an empty chart.
	RawRetention time.Duration
	M1Retention  time.Duration

	// MaxPoints caps how many points one history answer carries. Zero means
	// no cap.
	MaxPoints int
}

const (
	defaultFreshWindow      = 30 * time.Second
	defaultNodeOfflineAfter = 60 * time.Second
	defaultRawRetention     = 24 * time.Hour
	defaultM1Retention      = 30 * 24 * time.Hour
)

func (o Options) withDefaults() Options {
	if o.FreshWindow <= 0 {
		o.FreshWindow = defaultFreshWindow
	}
	if o.NodeOfflineAfter <= 0 {
		o.NodeOfflineAfter = defaultNodeOfflineAfter
	}
	if o.RawRetention <= 0 {
		o.RawRetention = defaultRawRetention
	}
	if o.M1Retention <= 0 {
		o.M1Retention = defaultM1Retention
	}
	return o
}

// DB wraps a SQLite connection with metrics-specific operations.
type DB struct {
	conn *sql.DB
	mu   sync.Mutex // serialize writes
	opts Options
}

// freshCutoff is the oldest timestamp a "latest" query accepts.
func (db *DB) freshCutoff() int64 {
	return time.Now().Add(-db.opts.FreshWindow).Unix()
}

// Open creates or opens the SQLite database.
func Open(dataDir string, opts Options) (*DB, error) {
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

	db := &DB{conn: conn, opts: opts.withDefaults()}
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
		version = 1
	}

	if version < 2 {
		if _, err := db.conn.Exec(migration002); err != nil {
			return fmt.Errorf("migration 002: %w", err)
		}
		log.Println("applied migration 002")
	}

	if version < 3 {
		if _, err := db.conn.Exec(migration003); err != nil {
			return fmt.Errorf("migration 003: %w", err)
		}
		log.Println("applied migration 003 (multi-node)")
	}

	if version < 4 {
		if _, err := db.conn.Exec(migration004); err != nil {
			return fmt.Errorf("migration 004: %w", err)
		}
		log.Println("applied migration 004 (rollup unique constraints)")
	}

	if version < 5 {
		if _, err := db.conn.Exec(migration005); err != nil {
			return fmt.Errorf("migration 005: %w", err)
		}
		log.Println("applied migration 005 (vllm metrics)")
	}

	if version < 6 {
		if _, err := db.conn.Exec(migration006); err != nil {
			return fmt.Errorf("migration 006: %w", err)
		}
		log.Println("applied migration 006 (alert events)")
	}

	if version < 7 {
		if _, err := db.conn.Exec(migration007); err != nil {
			return fmt.Errorf("migration 007: %w", err)
		}
		log.Println("applied migration 007 (vllm rollup)")
	}

	if version < 8 {
		if _, err := db.conn.Exec(migration008); err != nil {
			return fmt.Errorf("migration 008: %w", err)
		}
		log.Println("applied migration 008 (raw unique constraints)")
	}

	if version < 9 {
		if _, err := db.conn.Exec(migration009); err != nil {
			return fmt.Errorf("migration 009: %w", err)
		}
		log.Println("applied migration 009 (throttle reasons and ECC)")
	}

	if version < 10 {
		if _, err := db.conn.Exec(migration010); err != nil {
			return fmt.Errorf("migration 010: %w", err)
		}
		log.Println("applied migration 010 (throttle reasons in the rollups)")
	}

	if version < 11 {
		if _, err := db.conn.Exec(migration011); err != nil {
			return fmt.Errorf("migration 011: %w", err)
		}
		log.Println("applied migration 011 (power limit on throttle events)")
	}

	if version < 12 {
		if _, err := db.conn.Exec(migration012); err != nil {
			return fmt.Errorf("migration 012: %w", err)
		}
		log.Println("applied migration 012 (vLLM ratios may be absent)")
	}

	return nil
}

// Close checkpoints WAL and closes the database.
func (db *DB) Close() error {
	_, _ = db.conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return db.conn.Close()
}

// Conn returns the underlying sql.DB for advanced queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}
