// Package table holds the M0 table state and the fold that builds it from a
// session's event log. In M1 it grows into the entity model.
//
// Everything outside snapshot.go is pure: no clock, no randomness, no I/O.
// A test enforces this through the package's imports.
package table

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/njdaniel/chartroom/internal/eventlog"
)

// StateVersion is the version of State's shape and of the fold that builds
// it. Bump it whenever either changes, so stale snapshots are ignored.
//
// Version 2 added DiceRolls.
const StateVersion = 2

// ErrInconsistent means an event cannot apply to the current state: the log
// is out of order, or names a seat or token that does not exist.
var ErrInconsistent = errors.New("table: event does not apply to current state")

// Seat is one participant's place at the table.
type Seat struct {
	ID          eventlog.SeatID `json:"id"`
	DisplayName string          `json:"display_name"`
	Role        string          `json:"role"`
}

// Token is a piece on the table.
type Token struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Color  string  `json:"color"`
	Asset  string  `json:"asset,omitempty"`
	Pose   Pose    `json:"pose"`
	SizeIn float64 `json:"size_in"`
}

// Roll is one entry in the session's dice log.
type Roll struct {
	Seq  int64           `json:"seq"`
	Seat eventlog.SeatID `json:"seat"`
	DiceRolled
}

// State is a session's table, as of event Seq.
type State struct {
	Session  string `json:"session"`
	Seq      int64  `json:"seq"`
	Name     string `json:"name"`
	DiceSeed string `json:"dice_seed"` // secret; the visibility filter removes it
	// DiceRolls counts dice.rolled events so far. It is the index the next
	// roll passes to dice.Roll, which is how a restarted server continues
	// the same sequence of rolls.
	DiceRolls int64                    `json:"dice_rolls"`
	Seats     map[eventlog.SeatID]Seat `json:"seats"`
	Map       *MapLoaded               `json:"map"`
	Tokens    map[string]Token         `json:"tokens"`
	Rolls     []Roll                   `json:"rolls"`
}

// New returns the empty state of session, before its first event.
func New(session string) *State {
	return &State{
		Session: session,
		Seats:   map[eventlog.SeatID]Seat{},
		Tokens:  map[string]Token{},
		Rolls:   []Roll{},
	}
}

// Apply folds e into s. It checks that e follows s (same session, next seq)
// and is consistent with it before changing anything, so on error s is
// unchanged. e's data must already be at its type's latest version, as
// eventlog.Scan returns it.
func (s *State) Apply(e eventlog.Event) error {
	if e.Session != s.Session {
		return fmt.Errorf("%w: event for session %q applied to %q", ErrInconsistent, e.Session, s.Session)
	}
	if e.Seq != s.Seq+1 {
		return fmt.Errorf("%w: seq %d follows %d", ErrInconsistent, e.Seq, s.Seq)
	}
	if (e.Type == TypeSessionCreated) != (e.Seq == 1) {
		return fmt.Errorf("%w: %s at seq %d (session.created must be exactly seq 1)", ErrInconsistent, e.Type, e.Seq)
	}

	var err error
	switch e.Type {
	case TypeSessionCreated:
		err = apply(e, func(d SessionCreated) error {
			s.Name, s.DiceSeed = d.Name, d.DiceSeed
			return nil
		})
	case TypeSeatJoined:
		err = apply(e, func(d SeatJoined) error {
			if d.SeatID == "" || (d.Role != RoleGM && d.Role != RolePlayer) {
				return fmt.Errorf("%w: bad seat %q with role %q", ErrInconsistent, d.SeatID, d.Role)
			}
			if _, dup := s.Seats[d.SeatID]; dup {
				return fmt.Errorf("%w: seat %q joined twice", ErrInconsistent, d.SeatID)
			}
			s.Seats[d.SeatID] = Seat{ID: d.SeatID, DisplayName: d.DisplayName, Role: d.Role}
			return nil
		})
	case TypeMapLoaded:
		err = apply(e, func(d MapLoaded) error {
			s.Map = &d
			return nil
		})
	case TypeTokenPlaced:
		err = apply(e, func(d TokenPlaced) error {
			if d.TokenID == "" {
				return fmt.Errorf("%w: token with empty id", ErrInconsistent)
			}
			if _, dup := s.Tokens[d.TokenID]; dup {
				return fmt.Errorf("%w: token %q placed twice", ErrInconsistent, d.TokenID)
			}
			s.Tokens[d.TokenID] = Token{
				ID: d.TokenID, Label: d.Label, Color: d.Color, Asset: d.Asset,
				Pose: Pose{X: d.X, Y: d.Y, Rotation: d.Rotation}, SizeIn: d.SizeIn,
			}
			return nil
		})
	case TypeTokenMoved:
		err = apply(e, func(d TokenMoved) error {
			t, ok := s.Tokens[d.TokenID]
			if !ok {
				return fmt.Errorf("%w: move of unknown token %q", ErrInconsistent, d.TokenID)
			}
			t.Pose = d.To
			s.Tokens[d.TokenID] = t
			return nil
		})
	case TypeTokenRemoved:
		err = apply(e, func(d TokenRemoved) error {
			if _, ok := s.Tokens[d.TokenID]; !ok {
				return fmt.Errorf("%w: removal of unknown token %q", ErrInconsistent, d.TokenID)
			}
			delete(s.Tokens, d.TokenID)
			return nil
		})
	case TypeDiceRolled:
		err = apply(e, func(d DiceRolled) error {
			s.Rolls = append(s.Rolls, Roll{Seq: e.Seq, Seat: e.Seat, DiceRolled: d})
			s.DiceRolls++
			return nil
		})
	default:
		err = fmt.Errorf("%w: no fold for event type %q", ErrInconsistent, e.Type)
	}
	if err != nil {
		return fmt.Errorf("table: apply %s seq %d: %w", e.Type, e.Seq, err)
	}
	s.Seq = e.Seq
	return nil
}

// apply decodes e's data as T and passes it to fn, which must check
// everything before it changes state.
func apply[T any](e eventlog.Event, fn func(T) error) error {
	var d T
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return fn(d)
}
