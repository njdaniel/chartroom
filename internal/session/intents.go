package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/njdaniel/chartroom/internal/dice"
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/table"
)

// Limits on intent values. They keep nonsense out of the log; none is a
// game rule.
const (
	MaxIntentID  = 64     // bytes
	MaxLabel     = 64     // characters, for token and roll labels
	MaxName      = 100    // characters, for session names
	MaxSeatName  = 40     // characters, for display names
	MaxCoordIn   = 100000 // absolute value of x, y, and grid origins
	MaxMapIn     = 10000  // map width and height
	MaxSizeIn    = 1000   // token diameter and grid cell size
	maxIntentLen = 4096   // bytes of intent data
)

// Rejection is why an intent was refused. It becomes a rejected message, and
// nothing is appended to the log.
type Rejection struct {
	Code    string // one of the protocol.Code* constants
	Message string // for people, not programs
}

func (r *Rejection) Error() string { return r.Code + ": " + r.Message }

func reject(code, format string, args ...any) *Rejection {
	return &Rejection{Code: code, Message: fmt.Sprintf(format, args...)}
}

var (
	assetHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hexColor  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// plan checks in against the current state and returns the events it
// produces. It rolls dice if the intent needs them, but changes nothing: the
// caller appends and applies the drafts. A *Rejection means the intent is
// refused; any other error is the server's fault.
func (l *loop) plan(in protocol.Intent) ([]eventlog.Draft, error) {
	if in.ID == "" || len(in.ID) > MaxIntentID {
		return nil, reject(protocol.CodeBadIntent, "intent id must be 1 to %d bytes", MaxIntentID)
	}
	if len(in.Data) > maxIntentLen {
		return nil, reject(protocol.CodeBadIntent, "intent data is over %d bytes", maxIntentLen)
	}

	switch in.Kind {
	case protocol.KindMapLoad:
		return decode(in, l.planMapLoad)
	case protocol.KindTokenPlace:
		return decode(in, l.planTokenPlace)
	case protocol.KindTokenMove:
		return decode(in, l.planTokenMove)
	case protocol.KindTokenRemove:
		return decode(in, l.planTokenRemove)
	case protocol.KindDiceRoll:
		return decode(in, l.planDiceRoll)
	default:
		return nil, reject(protocol.CodeUnknownKind, "unknown intent kind %q", in.Kind)
	}
}

// decode reads in.Data strictly as T, so a misspelled field is an error
// rather than a silent zero, and passes it to fn.
func decode[T any](in protocol.Intent, fn func(T) ([]eventlog.Draft, error)) ([]eventlog.Draft, error) {
	var d T
	dec := json.NewDecoder(bytes.NewReader(in.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, reject(protocol.CodeBadIntent, "%s data: %v", in.Kind, err)
	}
	if dec.More() {
		return nil, reject(protocol.CodeBadIntent, "%s data: trailing data", in.Kind)
	}
	return fn(d)
}

func (l *loop) planMapLoad(d protocol.MapLoad) ([]eventlog.Draft, error) {
	if err := l.checkAsset(d.Asset); err != nil {
		return nil, err
	}
	if err := inRange("width_in", d.WidthIn, MaxMapIn); err != nil {
		return nil, err
	}
	if err := inRange("height_in", d.HeightIn, MaxMapIn); err != nil {
		return nil, err
	}
	if g := d.Grid; g != nil {
		if g.Kind != table.GridSquare && g.Kind != table.GridHex {
			return nil, reject(protocol.CodeBadIntent, "grid kind must be %q or %q", table.GridSquare, table.GridHex)
		}
		if err := inRange("grid size_in", g.SizeIn, MaxSizeIn); err != nil {
			return nil, err
		}
		if err := coords(g.OriginX, g.OriginY); err != nil {
			return nil, err
		}
	}
	return one(table.TypeMapLoaded, table.MapLoaded{
		Asset: d.Asset, WidthIn: d.WidthIn, HeightIn: d.HeightIn, Grid: d.Grid,
	}), nil
}

func (l *loop) planTokenPlace(d protocol.TokenPlace) ([]eventlog.Draft, error) {
	if err := text("label", d.Label, 0, MaxLabel); err != nil {
		return nil, err
	}
	if !hexColor.MatchString(d.Color) {
		return nil, reject(protocol.CodeBadIntent, "color must look like #a1b2c3")
	}
	if d.Asset != "" {
		if err := l.checkAsset(d.Asset); err != nil {
			return nil, err
		}
	}
	if err := coords(d.X, d.Y); err != nil {
		return nil, err
	}
	rot, err := rotation(d.Rotation)
	if err != nil {
		return nil, err
	}
	if err := inRange("size_in", d.SizeIn, MaxSizeIn); err != nil {
		return nil, err
	}
	return one(table.TypeTokenPlaced, table.TokenPlaced{
		TokenID: newID("t_"), Label: d.Label, Color: strings.ToLower(d.Color), Asset: d.Asset,
		X: d.X, Y: d.Y, Rotation: rot, SizeIn: d.SizeIn,
	}), nil
}

func (l *loop) planTokenMove(d protocol.TokenMove) ([]eventlog.Draft, error) {
	t, ok := l.st.Tokens[d.TokenID]
	if !ok {
		return nil, reject(protocol.CodeNoToken, "no token %q on the table", d.TokenID)
	}
	if err := coords(d.X, d.Y); err != nil {
		return nil, err
	}
	rot, err := rotation(d.Rotation)
	if err != nil {
		return nil, err
	}
	return one(table.TypeTokenMoved, table.TokenMoved{
		TokenID: d.TokenID, From: t.Pose, To: table.Pose{X: d.X, Y: d.Y, Rotation: rot},
	}), nil
}

func (l *loop) planTokenRemove(d protocol.TokenRemove) ([]eventlog.Draft, error) {
	if _, ok := l.st.Tokens[d.TokenID]; !ok {
		return nil, reject(protocol.CodeNoToken, "no token %q on the table", d.TokenID)
	}
	return one(table.TypeTokenRemoved, table.TokenRemoved{TokenID: d.TokenID}), nil
}

func (l *loop) planDiceRoll(d protocol.DiceRoll) ([]eventlog.Draft, error) {
	if err := text("label", d.Label, 0, MaxLabel); err != nil {
		return nil, err
	}
	expr, err := dice.Parse(d.Expr)
	if err != nil {
		return nil, reject(protocol.CodeBadDice, "%v", err)
	}
	// The roll's index is how many rolls the session has made, so the same
	// log always continues with the same results (ADR-002).
	res, err := dice.Roll(l.st.DiceSeed, l.st.DiceRolls, expr)
	if errors.Is(err, dice.ErrBadExpr) {
		return nil, reject(protocol.CodeBadDice, "%v", err)
	}
	if err != nil {
		return nil, fmt.Errorf("session %s: roll: %w", l.id, err)
	}
	terms := make([]table.DiceTerm, len(res.Terms))
	for i, t := range res.Terms {
		terms[i] = table.DiceTerm{Count: t.Count, Sides: t.Sides, Results: t.Results}
	}
	return one(table.TypeDiceRolled, table.DiceRolled{
		Expr: res.Expr, Label: d.Label, Terms: terms, Modifier: res.Modifier, Total: res.Total,
	}), nil
}

func (l *loop) checkAsset(hash string) error {
	if !assetHash.MatchString(hash) {
		return reject(protocol.CodeBadIntent, "asset must be the sha256 returned by the upload")
	}
	if l.m.cfg.AssetExists != nil && !l.m.cfg.AssetExists(hash) {
		return reject(protocol.CodeNoAsset, "no uploaded image %s", hash)
	}
	return nil
}

func one(typ string, data any) []eventlog.Draft {
	return []eventlog.Draft{{Type: typ, Data: data}}
}

// inRange checks 0 < v ≤ maxV.
func inRange(name string, v, maxV float64) error {
	if math.IsNaN(v) || v <= 0 || v > maxV {
		return reject(protocol.CodeBadIntent, "%s must be more than 0 and at most %g", name, maxV)
	}
	return nil
}

func coords(x, y float64) error {
	for _, v := range []float64{x, y} {
		if math.IsNaN(v) || math.Abs(v) > MaxCoordIn {
			return reject(protocol.CodeBadIntent, "coordinates must be within %g inches of the origin", float64(MaxCoordIn))
		}
	}
	return nil
}

// rotation normalizes degrees into [0, 360).
func rotation(deg float64) (float64, error) {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return 0, reject(protocol.CodeBadIntent, "rotation must be a number of degrees")
	}
	r := math.Mod(deg, 360)
	if r < 0 {
		r += 360
	}
	if r == 360 { // a tiny negative input rounds up
		r = 0
	}
	return r + 0, nil // + 0 turns -0 into 0
}

// text checks that s has min to max characters and no control characters.
func text(name, s string, minLen, maxLen int) error {
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < minLen || n > maxLen {
		return reject(protocol.CodeBadIntent, "%s must be %d to %d characters", name, minLen, maxLen)
	}
	if strings.ContainsFunc(s, unicode.IsControl) {
		return reject(protocol.CodeBadIntent, "%s must not contain control characters", name)
	}
	return nil
}
