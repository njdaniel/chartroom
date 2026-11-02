// Package visibility decides what each seat may see (invariant 4, ADR-003).
// Every event and every state sent to a client goes through a Filter, on the
// server. Fog of war (M3) and AI seats (M4) add rules here rather than new
// paths to the client.
//
// M0's only rule: no seat ever sees the session's dice seed.
package visibility

import (
	"encoding/json"
	"log/slog"
	"maps"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/table"
)

// Filter turns the server's view into one seat's view.
type Filter interface {
	// Event returns e as seat may see it, or false if seat may not see it
	// at all. st is the state after e. It must not change e.Data in place.
	Event(seat eventlog.SeatID, e eventlog.Event, st *table.State) (eventlog.Event, bool)
	// State returns st as seat may see it. The result shares nothing
	// mutable with st, so it can be used after the session moves on.
	State(seat eventlog.SeatID, st *table.State) protocol.State
}

// M0 is the M0 filter.
type M0 struct{}

// Event removes dice_seed from session.created and passes every other event
// through unchanged. An event it cannot read is dropped, not passed.
func (M0) Event(_ eventlog.SeatID, e eventlog.Event, _ *table.State) (eventlog.Event, bool) {
	if e.Type != table.TypeSessionCreated {
		return e, true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &fields); err != nil {
		slog.Error("visibility: unreadable session.created; dropping it", "session", e.Session, "err", err)
		return eventlog.Event{}, false
	}
	delete(fields, "dice_seed")
	data, err := json.Marshal(fields)
	if err != nil {
		slog.Error("visibility: re-encode session.created; dropping it", "session", e.Session, "err", err)
		return eventlog.Event{}, false
	}
	e.Data = data
	return e, true
}

// State copies everything but the dice seed and the roll counter, which
// protocol.State has no fields for.
func (M0) State(_ eventlog.SeatID, st *table.State) protocol.State {
	out := protocol.State{
		Session: st.Session,
		Seq:     st.Seq,
		Name:    st.Name,
		Seats:   maps.Clone(st.Seats),
		Tokens:  maps.Clone(st.Tokens),
		Rolls:   make([]table.Roll, len(st.Rolls)),
	}
	copy(out.Rolls, st.Rolls) // a roll's Terms never change once logged
	if st.Map != nil {
		m := *st.Map
		if m.Grid != nil {
			g := *m.Grid
			m.Grid = &g
		}
		out.Map = &m
	}
	return out
}
