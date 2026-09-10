// Package store provides the SQLite storage: schema, migrations and repositories.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	// modernc.org/sqlite is a pure-Go SQLite driver: no cgo, so the app
	// cross-compiles and builds with the plain toolchain.
	_ "modernc.org/sqlite"
)

// Storage errors.
var (
	// ErrNotFound is returned when the requested row does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrThreadExists is returned when a thread with the same
	// (channel_id, thread_ts) pair is already stored.
	ErrThreadExists = errors.New("store: thread already exists")
)

// Store is the SQLite-backed storage handle. It is safe for concurrent use.
type Store struct {
	db *sql.DB
	// now supplies timestamps; tests replace it to get deterministic values.
	now func() time.Time
}

// Open opens (creating it if needed) the database at path and applies every
// pending migration. Opening an already migrated database is a no-op, so the
// call is idempotent. The special path ":memory:" opens a private in-memory
// database.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn, err := dsnFor(path)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// SQLite writes serialize anyway; a single connection avoids "database is
	// locked" on concurrent writers and keeps in-memory databases coherent.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close() //nolint:errcheck // the open already failed

		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	s := &Store{db: db, now: time.Now}

	if err := s.migrate(ctx); err != nil {
		db.Close() //nolint:errcheck // the open already failed

		return nil, err
	}

	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}

	return nil
}

// Version reports the schema version currently stored in the database.
func (s *Store) Version(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}

	return v, nil
}

// dsnFor turns a file path into a driver DSN with the pragmas the app relies
// on: foreign keys (cascading deletes) and WAL for concurrent readers.
func dsnFor(path string) (string, error) {
	if path == "" {
		return "", errors.New("store: empty database path")
	}

	const pragmas = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	if path == ":memory:" {
		return path + pragmas, nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("store: resolve %s: %w", path, err)
	}

	return "file:" + abs + pragmas + "&_pragma=journal_mode(WAL)", nil
}

// migrate applies every migration the database has not seen yet, tracking
// progress in the user_version pragma.
func (s *Store) migrate(ctx context.Context) error {
	current, err := s.Version(ctx)
	if err != nil {
		return err
	}

	if current > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than supported %d", current, len(migrations))
	}

	for i := current; i < len(migrations); i++ {
		if err := s.applyMigration(ctx, i); err != nil {
			return err
		}
	}

	return nil
}

// applyMigration runs migration idx in a transaction and bumps user_version.
func (s *Store) applyMigration(ctx context.Context, idx int) error {
	m := migrations[idx]

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %q: begin: %w", m.name, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migration %q: %w", m.name, err)
		}
	}

	// PRAGMA does not accept bound parameters, and idx comes from our own
	// migration list, so the interpolation is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, idx+1)); err != nil {
		return fmt.Errorf("store: migration %q: set version: %w", m.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %q: commit: %w", m.name, err)
	}

	return nil
}
