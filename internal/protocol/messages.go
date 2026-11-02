package protocol

import (
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/table"
)

// Version is the protocol version in hello and welcome. A client with a
// different version is turned away with protocol_mismatch.
const Version = 1

// Message types: the "type" field of every WebSocket frame.
const (
	TypeHello    = "hello"    // client → server, first frame
	TypeIntent   = "intent"   // client → server
	TypeWelcome  = "welcome"  // server → client, reply to hello
	TypeAck      = "ack"      // server → client, an intent was applied
	TypeRejected = "rejected" // server → client, an intent was refused
	TypeEvent    = "event"    // server → client, one event from the log
	TypePresence = "presence" // server → client, who is connected
	TypeError    = "error"    // server → client, then the server closes
)

// Hello is the first frame a client sends.
type Hello struct {
	Type        string `json:"type"`
	Protocol    int    `json:"protocol"`
	Session     string `json:"session"`
	SeatToken   string `json:"seat_token,omitempty"` // from an earlier welcome, to take the same seat
	DisplayName string `json:"display_name"`         // used only when a new seat is created
}

// Welcome answers hello. After it, the server sends every event after Seq
// that this seat may see.
type Welcome struct {
	Type      string     `json:"type"`
	Protocol  int        `json:"protocol"`
	Seat      table.Seat `json:"seat"`
	SeatToken string     `json:"seat_token"` // keep it to come back to this seat
	State     State      `json:"state"`
	Seq       int64      `json:"seq"`
}

// State is a session's table as one seat may see it. It is table.State with
// the secrets left out: there is no field for the dice seed at all.
type State struct {
	Session string                         `json:"session"`
	Seq     int64                          `json:"seq"`
	Name    string                         `json:"name"`
	Seats   map[eventlog.SeatID]table.Seat `json:"seats"`
	Map     *table.MapLoaded               `json:"map"`
	Tokens  map[string]table.Token         `json:"tokens"`
	Rolls   []table.Roll                   `json:"rolls"`
}

// Ack says an intent was applied, and which events it produced. It comes
// after those events, and is sent even if the seat cannot see them.
type Ack struct {
	Type   string  `json:"type"`
	Intent string  `json:"intent"`
	Seqs   []int64 `json:"seqs"`
}

// Rejected says an intent was refused. Nothing was logged.
type Rejected struct {
	Type    string `json:"type"`
	Intent  string `json:"intent"`
	Code    string `json:"code"` // one of the Code* constants
	Message string `json:"message"`
}

// Event carries one event, already filtered for the seat receiving it.
type Event struct {
	Type  string         `json:"type"`
	Event eventlog.Event `json:"event"`
}

// Presence lists every seat in the session and whether it is connected. It
// is sent whenever someone connects or disconnects. It is not an event.
type Presence struct {
	Type  string         `json:"type"`
	Seats []PresenceSeat `json:"seats"`
}

// PresenceSeat is one seat in a presence message.
type PresenceSeat struct {
	ID          eventlog.SeatID `json:"id"`
	DisplayName string          `json:"display_name"`
	Online      bool            `json:"online"`
}

// Error is sent before the server closes a connection it cannot serve.
type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code"` // one of the Err* constants
	Message string `json:"message"`
}

// Error codes.
const (
	ErrProtocolMismatch = "protocol_mismatch" // hello.protocol is not Version
	ErrBadHello         = "bad_hello"         // the first frame was not a usable hello
	ErrUnknownSession   = "unknown_session"   // no session has that id
	ErrUnknownSeat      = "unknown_seat"      // the seat token is not for a seat in this session
	ErrBadMessage       = "bad_message"       // a frame that is not JSON with a known type
	ErrShuttingDown     = "shutting_down"     // the server is stopping; reconnect later
)
