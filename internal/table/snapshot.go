package table

// This is the one file in package table that does I/O. The fold in state.go
// must stay pure.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/njdaniel/chartroom/internal/eventlog"
)

// SnapshotEvery is how many events apart the session loop writes snapshots
// (ADR-002). It also writes one on clean shutdown.
const SnapshotEvery = 500

// ErrNoSession means a session has no events, so it does not exist.
var ErrNoSession = errors.New("table: no such session")

// SaveSnapshot stores s as the state of its session after event s.Seq.
// Saving the same seq twice keeps the newer copy.
func SaveSnapshot(ctx context.Context, db *sql.DB, s *State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("table: snapshot %s: encode: %w", s.Session, err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT OR REPLACE INTO snapshots (session_id, seq, state_v, state) VALUES (?, ?, ?, ?)`,
		s.Session, s.Seq, StateVersion, string(data))
	if err != nil {
		return fmt.Errorf("table: snapshot %s at seq %d: %w", s.Session, s.Seq, err)
	}
	return nil
}

// Load rebuilds session's current state: the newest snapshot written by
// this StateVersion, plus the events after it. With no usable snapshot it
// folds the whole log. Snapshots are only a cache, so one that will not
// decode is skipped, not fatal.
func Load(ctx context.Context, db *sql.DB, log *eventlog.Log, session string) (*State, error) {
	s := latestSnapshot(ctx, db, session)
	if s == nil {
		s = New(session)
	}
	if err := log.Scan(ctx, session, s.Seq, s.Apply); err != nil {
		return nil, err
	}
	if s.Seq == 0 {
		return nil, fmt.Errorf("%w %q", ErrNoSession, session)
	}
	return s, nil
}

func latestSnapshot(ctx context.Context, db *sql.DB, session string) *State {
	var seq int64
	var data string
	err := db.QueryRowContext(ctx,
		`SELECT seq, state FROM snapshots WHERE session_id = ? AND state_v = ?
		 ORDER BY seq DESC LIMIT 1`,
		session, StateVersion,
	).Scan(&seq, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		slog.Warn("table: could not read snapshot; folding from the start", "session", session, "err", err)
		return nil
	}

	s := New(session)
	if err := json.Unmarshal([]byte(data), s); err != nil || s.Session != session || s.Seq != seq {
		slog.Warn("table: ignoring unusable snapshot; folding from the start", "session", session, "seq", seq, "err", err)
		return nil
	}
	return s
}
