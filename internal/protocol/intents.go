// Package protocol holds the wire messages between chartroomd and its clients
// (ADR-003). It is the single source of truth for the client's TypeScript
// types, which cmd/schema-ts generates from it (#8).
//
// intents.go has the intents a client sends; messages.go has every other
// frame.
package protocol

import (
	"encoding/json"

	"github.com/njdaniel/chartroom/internal/table"
)

// Intent is a client's request to change the table. The server decides what,
// if anything, happens: every intent gets exactly one ack or rejected.
type Intent struct {
	Type string          `json:"type"` // TypeIntent on the wire
	ID   string          `json:"id"`   // chosen by the client, so it can match the reply
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// M0 intent kinds (ADR-003).
const (
	KindMapLoad     = "map.load"
	KindTokenPlace  = "token.place"
	KindTokenMove   = "token.move"
	KindTokenRemove = "token.remove"
	KindDiceRoll    = "dice.roll"
)

// MapLoad sets the table's map image. Produces map.loaded.
type MapLoad struct {
	Asset    string      `json:"asset"` // sha256 from POST /api/assets
	WidthIn  float64     `json:"width_in"`
	HeightIn float64     `json:"height_in"`
	Grid     *table.Grid `json:"grid"`
}

// TokenPlace puts a new token on the table. Produces token.placed; the
// server assigns the token's id.
type TokenPlace struct {
	Label    string  `json:"label"`
	Color    string  `json:"color"` // "#rrggbb"
	Asset    string  `json:"asset,omitempty"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
	SizeIn   float64 `json:"size_in"`
}

// TokenMove moves a token. Produces token.moved.
type TokenMove struct {
	TokenID  string  `json:"token_id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
}

// TokenRemove takes a token off the table. Produces token.removed.
type TokenRemove struct {
	TokenID string `json:"token_id"`
}

// DiceRoll rolls a dice expression such as "2d6+1d4-1". Produces dice.rolled.
type DiceRoll struct {
	Expr  string `json:"expr"`
	Label string `json:"label,omitempty"`
}

// Rejection codes, sent in rejected.code.
const (
	CodeBadIntent   = "bad_intent"    // malformed, or a value out of range
	CodeUnknownKind = "unknown_kind"  // not an intent kind this server knows
	CodeForbidden   = "forbidden"     // authorize refused it
	CodeNoToken     = "unknown_token" // token_id is not on the table
	CodeNoAsset     = "unknown_asset" // no uploaded image has that hash
	CodeBadDice     = "bad_dice_expr" // the dice expression does not parse or is out of range
)
