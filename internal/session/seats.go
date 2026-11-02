package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/table"
)

// ErrUnknownSeat means a seat token does not name a seat in the session.
var ErrUnknownSeat = errors.New("session: no seat with that token")

// Join adds a new seat to session and returns it with its seat token. The
// first seat is the GM; later seats are players. The token is how a browser
// comes back to the same seat (SeatForToken). It identifies a seat; it does
// not authenticate a person (ADR-003).
func (m *Manager) Join(ctx context.Context, session, displayName string) (table.Seat, string, error) {
	displayName = strings.TrimSpace(displayName)
	if err := text("display name", displayName, 1, MaxSeatName); err != nil {
		return table.Seat{}, "", fmt.Errorf("%w: %s", ErrBadName, err.(*Rejection).Message)
	}
	var seat table.Seat
	token := newID("") // 128 random bits
	var err error
	if derr := m.do(ctx, session, func(l *loop) {
		role := table.RolePlayer
		if len(l.st.Seats) == 0 {
			role = table.RoleGM
		}
		id := eventlog.SeatID(newID("seat_"))
		// The token goes in first. If the seat then fails to join, the
		// token names a seat that does not exist, which SeatForToken's
		// caller rejects when it finds no such seat in the state.
		if _, err = m.cfg.DB.ExecContext(ctx,
			`INSERT INTO seat_tokens (token_hash, session_id, seat_id) VALUES (?, ?, ?)`,
			hashToken(token), session, string(id)); err != nil {
			err = fmt.Errorf("session %s: save seat token: %w", session, err)
			return
		}
		if _, err = l.commit(ctx, id, "", eventlog.Draft{
			Type: table.TypeSeatJoined,
			Data: table.SeatJoined{SeatID: id, DisplayName: displayName, Role: role},
		}); err == nil {
			seat = l.st.Seats[id]
		}
	}); derr != nil {
		return table.Seat{}, "", derr
	}
	if err != nil {
		return table.Seat{}, "", err
	}
	return seat, token, nil
}

// SeatForToken returns the seat in session that token was issued for.
func (m *Manager) SeatForToken(ctx context.Context, session, token string) (eventlog.SeatID, error) {
	if token == "" {
		return "", ErrUnknownSeat
	}
	var id string
	err := m.cfg.DB.QueryRowContext(ctx,
		`SELECT seat_id FROM seat_tokens WHERE token_hash = ? AND session_id = ?`,
		hashToken(token), session).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnknownSeat
	}
	if err != nil {
		return "", fmt.Errorf("session %s: look up seat token: %w", session, err)
	}
	return eventlog.SeatID(id), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
