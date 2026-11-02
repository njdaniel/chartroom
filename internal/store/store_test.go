package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesPrivateWALDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chartroom.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 600", perm)
	}

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Errorf("user_version = %d, want %d", version, len(migrations))
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chartroom.db")

	for range 2 {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chartroom.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("Open = %v, want a newer-schema error", err)
	}
}

func TestEventsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.ExecContext(ctx, `INSERT INTO events (session_id, seq, id, at, type, data_v, data)
		VALUES ('s_1', 1, 'e_1', '2026-11-02T00:00:00Z', 'test.happened', 1, '{}')`)
	if err != nil {
		t.Fatal(err)
	}

	for _, stmt := range []string{
		`UPDATE events SET data = '{"x":1}' WHERE id = 'e_1'`,
		`DELETE FROM events WHERE id = 'e_1'`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: err = %v, want append-only error", stmt, err)
		}
	}
}
