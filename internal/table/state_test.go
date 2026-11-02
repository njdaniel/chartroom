package table

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/njdaniel/chartroom/internal/eventlog"
)

// ev builds a v1 event for session s_1 at seq.
func ev(seq int64, typ string, data any) eventlog.Event {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return eventlog.Event{V: 1, ID: "e_" + strconv.FormatInt(seq, 10), Session: "s_1", Seq: seq, Type: typ, DataV: 1, Data: b}
}

func mustApply(t *testing.T, s *State, events ...eventlog.Event) {
	t.Helper()
	for _, e := range events {
		if err := s.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
}

func asJSON(t *testing.T, s *State) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyBuildsTable(t *testing.T) {
	s := New("s_1")
	roll := DiceRolled{Expr: "2d6+3", Terms: []DiceTerm{{Count: 2, Sides: 6, Results: []int{4, 2}}}, Modifier: 3, Total: 9}
	mustApply(t, s,
		ev(1, TypeSessionCreated, SessionCreated{Name: "Friday game", DiceSeed: strings.Repeat("ab", 32)}),
		ev(2, TypeSeatJoined, SeatJoined{SeatID: "seat_a", DisplayName: "Nick", Role: RoleGM}),
		ev(3, TypeMapLoaded, MapLoaded{Asset: "abc", WidthIn: 48, HeightIn: 36, Grid: &Grid{Kind: GridSquare, SizeIn: 1}}),
		ev(4, TypeTokenPlaced, TokenPlaced{TokenID: "t_1", Label: "Orc", Color: "#3a3", X: 1, Y: 2, SizeIn: 1}),
		ev(5, TypeTokenPlaced, TokenPlaced{TokenID: "t_2", Label: "Elf", X: 5, Y: 5, SizeIn: 1}),
		ev(6, TypeTokenMoved, TokenMoved{TokenID: "t_1", From: Pose{X: 1, Y: 2}, To: Pose{X: 3.5, Y: 4, Rotation: 90}}),
		ev(7, TypeTokenRemoved, TokenRemoved{TokenID: "t_2"}),
	)
	e8 := ev(8, TypeDiceRolled, roll)
	e8.Seat = "seat_a"
	mustApply(t, s, e8)

	if s.Seq != 8 || s.Name != "Friday game" || len(s.DiceSeed) != 64 {
		t.Errorf("session fields: seq %d, name %q, seed %q", s.Seq, s.Name, s.DiceSeed)
	}
	if s.Seats["seat_a"].Role != RoleGM {
		t.Errorf("seats = %+v", s.Seats)
	}
	if s.Map == nil || s.Map.WidthIn != 48 || s.Map.Grid.Kind != GridSquare {
		t.Errorf("map = %+v", s.Map)
	}
	if len(s.Tokens) != 1 || s.Tokens["t_1"].Pose != (Pose{X: 3.5, Y: 4, Rotation: 90}) {
		t.Errorf("tokens = %+v", s.Tokens)
	}
	if len(s.Rolls) != 1 || s.Rolls[0].Seq != 8 || s.Rolls[0].Seat != "seat_a" || s.Rolls[0].Total != 9 {
		t.Errorf("rolls = %+v", s.Rolls)
	}
}

func TestApplyRejectsInconsistentEvents(t *testing.T) {
	created := ev(1, TypeSessionCreated, SessionCreated{Name: "x"})
	base := []eventlog.Event{
		created,
		ev(2, TypeSeatJoined, SeatJoined{SeatID: "seat_a", Role: RolePlayer}),
		ev(3, TypeTokenPlaced, TokenPlaced{TokenID: "t_1"}),
	}
	otherSession := ev(4, TypeTokenRemoved, TokenRemoved{TokenID: "t_1"})
	otherSession.Session = "s_2"

	cases := map[string]eventlog.Event{
		"seq gap":                  ev(5, TypeTokenRemoved, TokenRemoved{TokenID: "t_1"}),
		"repeated seq":             ev(3, TypeTokenRemoved, TokenRemoved{TokenID: "t_1"}),
		"other session":            otherSession,
		"second session.created":   ev(4, TypeSessionCreated, SessionCreated{}),
		"duplicate seat":           ev(4, TypeSeatJoined, SeatJoined{SeatID: "seat_a", Role: RolePlayer}),
		"bad role":                 ev(4, TypeSeatJoined, SeatJoined{SeatID: "seat_b", Role: "admin"}),
		"duplicate token":          ev(4, TypeTokenPlaced, TokenPlaced{TokenID: "t_1"}),
		"move unknown token":       ev(4, TypeTokenMoved, TokenMoved{TokenID: "t_9"}),
		"remove unknown token":     ev(4, TypeTokenRemoved, TokenRemoved{TokenID: "t_9"}),
		"unknown type":             ev(4, "token.exploded", struct{}{}),
		"data that will not parse": {V: 1, Session: "s_1", Seq: 4, Type: TypeTokenRemoved, DataV: 1, Data: json.RawMessage(`[1]`)},
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			s := New("s_1")
			mustApply(t, s, base...)
			before := asJSON(t, s)

			if err := s.Apply(bad); err == nil {
				t.Fatal("Apply succeeded, want an error")
			} else if name != "data that will not parse" && !errors.Is(err, ErrInconsistent) {
				t.Fatalf("err = %v, want ErrInconsistent", err)
			}
			if after := asJSON(t, s); after != before {
				t.Fatalf("state changed by a rejected event:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	t.Run("first event is not session.created", func(t *testing.T) {
		if err := New("s_1").Apply(ev(1, TypeTokenPlaced, TokenPlaced{TokenID: "t_1"})); !errors.Is(err, ErrInconsistent) {
			t.Fatalf("err = %v, want ErrInconsistent", err)
		}
	})
}

func TestApplyIsDeterministic(t *testing.T) {
	events := genEvents(7, 300)
	a, b := New("s_1"), New("s_1")
	mustApply(t, a, events...)
	mustApply(t, b, events...)
	if asJSON(t, a) != asJSON(t, b) {
		t.Fatal("the same events folded to different states")
	}
}

// The fold must not read the clock, randomness, or do I/O (invariants 2, 3,
// and 5). Only snapshot.go may do I/O; this checks every other file's imports.
func TestFoldImportsArePure(t *testing.T) {
	allowed := map[string]bool{
		"encoding/json": true, "errors": true, "fmt": true,
		"github.com/njdaniel/chartroom/internal/eventlog": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "snapshot.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %q; the fold may only import %v", f, path, allowed)
			}
		}
	}
}
