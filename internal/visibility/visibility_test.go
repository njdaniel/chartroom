package visibility

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/table"
)

const seed = "5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed5eed"

func event(seq int64, typ string, data any) eventlog.Event {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return eventlog.Event{V: 1, ID: "e_1", Session: "s_1", Seq: seq, Type: typ, DataV: 1, Data: b}
}

func TestEventRemovesTheSeed(t *testing.T) {
	e := event(1, table.TypeSessionCreated, table.SessionCreated{Name: "Friday game", DiceSeed: seed})
	orig := string(e.Data)
	got, ok := M0{}.Event("seat_1", e, table.New("s_1"))
	if !ok {
		t.Fatal("session.created was dropped")
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), seed) || strings.Contains(string(b), "dice_seed") {
		t.Fatalf("filtered event still has the seed: %s", b)
	}
	if !strings.Contains(string(got.Data), `"Friday game"`) {
		t.Fatalf("filtered event lost the name: %s", got.Data)
	}
	if string(e.Data) != orig {
		t.Fatal("filter changed the original event's data")
	}
}

func TestEventDropsWhatItCannotRead(t *testing.T) {
	e := eventlog.Event{Session: "s_1", Seq: 1, Type: table.TypeSessionCreated, Data: json.RawMessage(`"` + seed + `"`)}
	if _, ok := (M0{}).Event("seat_1", e, nil); ok {
		t.Fatal("an unreadable session.created was passed through")
	}
}

func TestEventPassesOtherEvents(t *testing.T) {
	for _, e := range []eventlog.Event{
		event(2, table.TypeSeatJoined, table.SeatJoined{SeatID: "seat_1", DisplayName: "Nick", Role: table.RoleGM}),
		event(3, table.TypeTokenPlaced, table.TokenPlaced{TokenID: "t_1", Color: "#000000", SizeIn: 1}),
		event(4, table.TypeDiceRolled, table.DiceRolled{Expr: "1d20", Terms: []table.DiceTerm{{Count: 1, Sides: 20, Results: []int{7}}}, Total: 7}),
	} {
		got, ok := M0{}.Event("seat_1", e, nil)
		if !ok || !reflect.DeepEqual(got, e) {
			t.Errorf("%s changed: %+v", e.Type, got)
		}
	}
}

func TestStateHasNoSeed(t *testing.T) {
	st := table.New("s_1")
	for i, e := range []eventlog.Event{
		event(1, table.TypeSessionCreated, table.SessionCreated{Name: "Friday game", DiceSeed: seed}),
		event(2, table.TypeSeatJoined, table.SeatJoined{SeatID: "seat_1", DisplayName: "Nick", Role: table.RoleGM}),
		event(3, table.TypeMapLoaded, table.MapLoaded{Asset: strings.Repeat("a", 64), WidthIn: 4, HeightIn: 4, Grid: &table.Grid{Kind: table.GridHex, SizeIn: 1}}),
		event(4, table.TypeTokenPlaced, table.TokenPlaced{TokenID: "t_1", Color: "#000000", SizeIn: 1}),
		event(5, table.TypeDiceRolled, table.DiceRolled{Expr: "1d20", Total: 7}),
	} {
		e.Seat = "seat_1"
		if err := st.Apply(e); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}

	got := M0{}.State("seat_1", st)
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), seed) || strings.Contains(string(b), "dice_seed") {
		t.Fatalf("filtered state has the seed: %s", b)
	}
	if got.Name != "Friday game" || got.Seq != 5 || len(got.Seats) != 1 || len(got.Tokens) != 1 || len(got.Rolls) != 1 || got.Map.Grid.Kind != table.GridHex {
		t.Fatalf("filtered state lost something: %s", b)
	}

	// It is a copy: later changes to the table do not reach it.
	st.Tokens["t_2"] = table.Token{ID: "t_2"}
	st.Seats["seat_2"] = table.Seat{ID: "seat_2"}
	st.Map.Grid.SizeIn = 99
	st.Rolls[0].Total = 99
	if len(got.Tokens) != 1 || len(got.Seats) != 1 || got.Map.Grid.SizeIn != 1 || got.Rolls[0].Total != 7 {
		t.Fatal("filtered state shares data with the table")
	}
}
