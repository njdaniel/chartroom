package table

import "github.com/njdaniel/chartroom/internal/eventlog"

// M0 event types (ADR-002). Their payload shapes are part of the saved-game
// format: changing a field is an ADR-000 sign-off item.
const (
	TypeSessionCreated = "session.created"
	TypeSeatJoined     = "seat.joined"
	TypeMapLoaded      = "map.loaded"
	TypeTokenPlaced    = "token.placed"
	TypeTokenMoved     = "token.moved"
	TypeTokenRemoved   = "token.removed"
	TypeDiceRolled     = "dice.rolled"
)

// Register adds the M0 event types to reg, each at data version 1.
func Register(reg *eventlog.Registry) {
	for _, typ := range []string{
		TypeSessionCreated, TypeSeatJoined, TypeMapLoaded,
		TypeTokenPlaced, TypeTokenMoved, TypeTokenRemoved, TypeDiceRolled,
	} {
		reg.Register(typ, 1, nil)
	}
}

// SessionCreated is always the first event in a session (seq 1).
type SessionCreated struct {
	Name     string `json:"name"`
	DiceSeed string `json:"dice_seed"` // 64 hex chars; secret, never sent to a seat
}

// Seat roles.
const (
	RoleGM     = "gm"
	RolePlayer = "player"
)

// SeatJoined adds a seat to the session.
type SeatJoined struct {
	SeatID      eventlog.SeatID `json:"seat_id"`
	DisplayName string          `json:"display_name"`
	Role        string          `json:"role"`
}

// Grid kinds. A grid is drawn only in M0; nothing snaps to it.
const (
	GridSquare = "square"
	GridHex    = "hex"
)

// Grid is an optional overlay on the map.
type Grid struct {
	Kind    string  `json:"kind"`
	SizeIn  float64 `json:"size_in"`
	OriginX float64 `json:"origin_x"`
	OriginY float64 `json:"origin_y"`
}

// MapLoaded sets (or replaces) the table's map image.
type MapLoaded struct {
	Asset    string  `json:"asset"` // sha256 of the image
	WidthIn  float64 `json:"width_in"`
	HeightIn float64 `json:"height_in"`
	Grid     *Grid   `json:"grid"`
}

// TokenPlaced puts a new token on the table. Coordinates are inches;
// rotation is degrees clockwise from north.
type TokenPlaced struct {
	TokenID  string  `json:"token_id"`
	Label    string  `json:"label"`
	Color    string  `json:"color"`
	Asset    string  `json:"asset,omitempty"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
	SizeIn   float64 `json:"size_in"` // diameter
}

// Pose is a position and facing on the table.
type Pose struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
}

// TokenMoved moves a token. The server fills From from current state.
type TokenMoved struct {
	TokenID string `json:"token_id"`
	From    Pose   `json:"from"`
	To      Pose   `json:"to"`
}

// TokenRemoved takes a token off the table.
type TokenRemoved struct {
	TokenID string `json:"token_id"`
}

// DiceTerm is one NdM term of a roll and the result of each die.
type DiceTerm struct {
	Count   int   `json:"count"`
	Sides   int   `json:"sides"`
	Results []int `json:"results"`
}

// DiceRolled records a roll: what was asked for, every die, and the total.
type DiceRolled struct {
	Expr     string     `json:"expr"`
	Label    string     `json:"label,omitempty"`
	Terms    []DiceTerm `json:"terms"`
	Modifier int        `json:"modifier"`
	Total    int        `json:"total"`
}
