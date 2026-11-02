// Package eventlog is Chartroom's append-only event log (ADR-002). Every
// game state change is an Event appended here; state is a fold over a
// session's events in seq order.
package eventlog

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EnvelopeVersion is the envelope shape this code reads and writes. Only an
// ADR changes it.
const EnvelopeVersion = 1

// SeatID identifies a seat. The empty SeatID means a system event, and
// encodes as JSON null.
type SeatID string

// MarshalJSON encodes the empty SeatID as null.
func (s SeatID) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(s))
}

// UnmarshalJSON decodes null as the empty SeatID.
func (s *SeatID) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*s = ""
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	*s = SeatID(str)
	return nil
}

// Event is one entry in a session's log: ADR-002's envelope.
type Event struct {
	V       int             `json:"v"`
	ID      string          `json:"id"`
	Session string          `json:"session"`
	Seq     int64           `json:"seq"`
	At      time.Time       `json:"at"` // display and audit only; never read by a fold
	Seat    SeatID          `json:"seat"`
	Cause   string          `json:"cause,omitempty"`
	Type    string          `json:"type"`
	DataV   int             `json:"data_v"`
	Data    json.RawMessage `json:"data"`
}

// Draft is an event to append: its type and payload. Append fills in the
// rest of the envelope.
type Draft struct {
	Type string
	Data any // encoded as a JSON object
}

// Log reads and appends events in one database.
type Log struct {
	db  *sql.DB
	reg *Registry
	now func() time.Time
}

// New returns a Log over db (opened with store.Open) that accepts the types in reg.
func New(db *sql.DB, reg *Registry) *Log {
	return &Log{db: db, reg: reg, now: time.Now}
}

// Append writes drafts to the end of session's log as one transaction: either
// every draft is appended, with consecutive seqs, or none is. seat is the seat
// whose intent caused them ("" for the system), and cause is that intent's id
// ("" if none). It returns the appended events.
func (l *Log) Append(ctx context.Context, session string, seat SeatID, cause string, drafts ...Draft) ([]Event, error) {
	if session == "" {
		return nil, errors.New("eventlog: append: empty session id")
	}
	if len(drafts) == 0 {
		return nil, errors.New("eventlog: append: no events")
	}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("eventlog: append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var last int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM events WHERE session_id = ?`, session,
	).Scan(&last); err != nil {
		return nil, fmt.Errorf("eventlog: append: read last seq: %w", err)
	}

	at := l.now().UTC()
	events := make([]Event, 0, len(drafts))
	for i, d := range drafts {
		dataV, err := l.reg.latest(d.Type)
		if err != nil {
			return nil, fmt.Errorf("eventlog: append: %w", err)
		}
		data, err := json.Marshal(d.Data)
		if err != nil {
			return nil, fmt.Errorf("eventlog: append %s: encode data: %w", d.Type, err)
		}
		if len(data) == 0 || data[0] != '{' {
			return nil, fmt.Errorf("eventlog: append %s: data must be a JSON object", d.Type)
		}

		e := Event{
			V:       EnvelopeVersion,
			ID:      newEventID(),
			Session: session,
			Seq:     last + int64(i) + 1,
			At:      at,
			Seat:    seat,
			Cause:   cause,
			Type:    d.Type,
			DataV:   dataV,
			Data:    data,
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events (session_id, seq, id, at, seat_id, cause, type, data_v, data)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Session, e.Seq, e.ID, e.At.Format(time.RFC3339Nano),
			nullable(string(e.Seat)), nullable(e.Cause), e.Type, e.DataV, string(e.Data),
		); err != nil {
			return nil, fmt.Errorf("eventlog: append %s: %w", d.Type, err)
		}
		events = append(events, e)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("eventlog: append: commit: %w", err)
	}
	return events, nil
}

// Scan calls fn for each event in session with seq greater than after, in
// seq order, with data upcast to its type's latest version. It stops at the
// first error, from the log or from fn.
func (l *Log) Scan(ctx context.Context, session string, after int64, fn func(Event) error) error {
	rows, err := l.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE session_id = ? AND seq > ? ORDER BY seq`,
		session, after)
	if err != nil {
		return fmt.Errorf("eventlog: read %s: %w", session, err)
	}
	defer rows.Close()

	for rows.Next() {
		e, err := l.scanEvent(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("eventlog: read %s: %w", session, err)
	}
	return nil
}

// Read returns the events in session with seq greater than after; see Scan.
func (l *Log) Read(ctx context.Context, session string, after int64) ([]Event, error) {
	var events []Event
	err := l.Scan(ctx, session, after, func(e Event) error {
		events = append(events, e)
		return nil
	})
	return events, err
}

// Firsts returns the first event (seq 1) of every session, oldest first,
// with data upcast to its type's latest version.
func (l *Log) Firsts(ctx context.Context) ([]Event, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT `+eventColumns+` FROM events WHERE seq = 1 ORDER BY at, session_id`)
	if err != nil {
		return nil, fmt.Errorf("eventlog: list sessions: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		e, err := l.scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventlog: list sessions: %w", err)
	}
	return events, nil
}

// eventColumns is the column list scanEvent reads.
const eventColumns = `session_id, seq, id, at, seat_id, cause, type, data_v, data`

// scanEvent reads one row of eventColumns and upcasts its data.
func (l *Log) scanEvent(rows *sql.Rows) (Event, error) {
	e := Event{V: EnvelopeVersion}
	var at, data string
	var seat, cause sql.NullString
	if err := rows.Scan(&e.Session, &e.Seq, &e.ID, &at, &seat, &cause, &e.Type, &e.DataV, &data); err != nil {
		return Event{}, fmt.Errorf("eventlog: read: %w", err)
	}
	var err error
	if e.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return Event{}, fmt.Errorf("eventlog: read %s seq %d: bad time: %w", e.Session, e.Seq, err)
	}
	e.Seat, e.Cause, e.Data = SeatID(seat.String), cause.String, json.RawMessage(data)
	if err := l.reg.upcast(&e); err != nil {
		return Event{}, fmt.Errorf("eventlog: read %s seq %d: %w", e.Session, e.Seq, err)
	}
	return e, nil
}

// newEventID returns "e_" and 128 random bits in hex. Ids are not game
// randomness (invariant 3 covers dice only), so crypto/rand is used directly.
func newEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (Go 1.24+)
	return "e_" + hex.EncodeToString(b[:])
}

func nullable(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
