// Package store opens Chartroom's SQLite database and keeps its schema
// current. Every table's migrations live here, so the schema has one ordered
// history; the packages that use a table (such as eventlog) own its queries.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// migrations is the schema history. Each entry is applied once, in order, in
// its own transaction; PRAGMA user_version records how many have run.
// Never edit or reorder an entry once it has merged: add a new one.
var migrations = [][]string{
	// 1: the append-only event log (ADR-002).
	{
		`CREATE TABLE events (
			session_id TEXT    NOT NULL,
			seq        INTEGER NOT NULL,
			id         TEXT    NOT NULL UNIQUE,
			at         TEXT    NOT NULL,
			seat_id    TEXT,
			cause      TEXT,
			type       TEXT    NOT NULL,
			data_v     INTEGER NOT NULL,
			data       TEXT    NOT NULL,
			PRIMARY KEY (session_id, seq)
		) WITHOUT ROWID`,
		`CREATE TRIGGER events_no_update BEFORE UPDATE ON events
		BEGIN SELECT RAISE(ABORT, 'events are append-only'); END`,
		`CREATE TRIGGER events_no_delete BEFORE DELETE ON events
		BEGIN SELECT RAISE(ABORT, 'events are append-only'); END`,
	},
}

// Open opens (creating if needed) the database at path, in WAL mode, and
// applies any pending migrations.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	// The log holds the dice seeds, which players must not read. Create the
	// file private before SQLite does, so its -wal and -shm files match.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}
	_ = f.Close()

	// journal_mode is stored in the file; the other pragmas are
	// per-connection, so they go in the DSN to cover every pooled connection.
	// _txlock=immediate takes the write lock when a transaction begins, so
	// concurrent writers queue on busy_timeout instead of failing midway.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("store: database schema version %d is newer than this binary (max %d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if err := applyMigration(ctx, db, i); err != nil {
			return fmt.Errorf("store: apply migration %d: %w", i+1, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, i int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range migrations[i] {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// PRAGMA takes no bound parameters; i is an int we control.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
		return err
	}
	return tx.Commit()
}
