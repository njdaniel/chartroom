package session

import (
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/table"
)

// Authorizer decides whether seat may make intent in. It is the single place
// seat permissions live (ADR-003). It runs on the session loop before the
// intent is validated, with the current state, which it must not change.
//
// A nil error allows the intent. A *Rejection is sent as is; any other error
// becomes a forbidden rejection with that error's text.
type Authorizer func(seat eventlog.SeatID, in protocol.Intent, st *table.State) error

// AllowSeated is the M0 policy: any seat in the session may make any intent.
// There are no GM-only actions yet (accepted on PR #1).
func AllowSeated(seat eventlog.SeatID, _ protocol.Intent, st *table.State) error {
	if _, ok := st.Seats[seat]; !ok {
		return reject(protocol.CodeForbidden, "you do not have a seat in this session")
	}
	return nil
}
