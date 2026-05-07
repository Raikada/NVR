// Package store wraps the recorder's local SQLite database used for
// pre-pairing operator authentication (LocalUser records).
//
// SQLite + modernc.org/sqlite + pressly/goose v3 + plain database/sql
// mirrors the Management Server's pattern (management/internal/store/)
// per ADR 0001 + ADR 0002 — the same pattern carries over verbatim
// because the recorder's pre-pairing auth surface and the MS's auth
// surface have identical operational shape. See AGENTS.md §8 for the
// "no new dependencies without justification" gate; modernc.org/sqlite
// and pressly/goose v3 satisfy it because they're already in the
// management-tier go.mod and chosen for the same reasons (pure-Go,
// embedded migrations, no external DB to provision).
//
// Lifecycle: Open(path) opens or creates a SQLite file at path,
// applies all pending migrations, and returns a ready Store. The
// recorder calls Open exactly once at startup (Core); the store is
// closed at shutdown.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is a thin wrapper around the SQLite handle plus per-table
// repository accessors.
type Store struct {
	DB *sql.DB

	LocalUsers         *LocalUsersRepo
	OnvifSubscriptions *OnvifSubscriptionsRepo
	Roles              *RolesRepo
	CameraGroups       *CameraGroupsRepo
	Cameras            *CamerasRepo
	CameraCredentials  *CameraCredentialsRepo
	CameraCapabilities *CameraCapabilitiesRepo
}

// Open opens (or creates) the SQLite database at path, applies all
// pending migrations, and returns a ready-to-use Store. Pass ":memory:"
// for an in-memory database (tests).
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	if path == ":memory:" {
		dsn = path + "?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite is single-writer; one connection avoids spurious
	// "database is locked" errors at high write concurrency. Reads
	// still parallelize via WAL's reader/writer separation.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{DB: db}
	s.LocalUsers = &LocalUsersRepo{db: db}
	s.OnvifSubscriptions = &OnvifSubscriptionsRepo{db: db}
	s.Roles = &RolesRepo{db: db}
	s.CameraGroups = &CameraGroupsRepo{db: db}
	s.Cameras = &CamerasRepo{db: db}
	s.CameraCredentials = &CameraCredentialsRepo{db: db}
	s.CameraCapabilities = &CameraCapabilitiesRepo{db: db}
	return s, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

// WithTx runs fn inside a transaction. If fn returns an error, the
// transaction is rolled back; otherwise it is committed.
func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func runMigrations(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("goose set dialect: %w", err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// Now returns time.Now() truncated to RFC 3339 millisecond precision.
// Centralized so all timestamps the recorder writes have the same
// shape (matches the MS).
func Now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// FormatTime formats a time.Time the way the recorder persists
// timestamps.
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// ParseTime parses a stored timestamp.
func ParseTime(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05.000Z07:00", s)
}

// rowScanner abstracts *sql.Row and *sql.Rows so per-row scan helpers
// can serve both single-row and multi-row callers.
type rowScanner interface {
	Scan(...any) error
}

// boolToInt encodes a bool as a 0/1 INTEGER for SQLite STRICT tables.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isConstraintErr returns true if err is a SQLite constraint
// violation. The driver doesn't give us a structured error type, so
// we string-match — best-effort. Mirrors the management-tier helper.
func isConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "constraint") ||
		strings.Contains(msg, "UNIQUE") ||
		strings.Contains(msg, "PRIMARY KEY")
}

// ErrLocalUserExists is returned by Insert when the username is taken.
var ErrLocalUserExists = errors.New("local user with that username already exists")

// ErrLocalUserNotFound is returned by per-id mutators when no row matches.
var ErrLocalUserNotFound = errors.New("local user not found")
