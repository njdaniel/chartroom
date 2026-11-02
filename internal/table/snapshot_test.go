package table

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/store"
)

// genEvents returns a random but consistent M0 event sequence of n events
// for session s_1, reproducible from seed. (Test-only randomness.)
func genEvents(seed uint64, n int) []eventlog.Event {
	r := rand.New(rand.NewPCG(seed, 0))
	events := []eventlog.Event{ev(1, TypeSessionCreated, SessionCreated{Name: "generated", DiceSeed: "00"})}
	var seats []eventlog.SeatID
	var tokens []string
	nextToken := 0
	pose := func() Pose { return Pose{X: r.Float64() * 48, Y: r.Float64() * 36, Rotation: float64(r.IntN(360))} }

	for seq := int64(2); seq <= int64(n); seq++ {
		var e eventlog.Event
		switch k := r.IntN(10); {
		case k == 0 || len(seats) == 0:
			id := eventlog.SeatID(fmt.Sprintf("seat_%d", len(seats)))
			role := RolePlayer
			if len(seats) == 0 {
				role = RoleGM
			}
			seats = append(seats, id)
			e = ev(seq, TypeSeatJoined, SeatJoined{SeatID: id, DisplayName: string(id), Role: role})
		case k == 1:
			var g *Grid
			if r.IntN(2) == 0 {
				g = &Grid{Kind: GridHex, SizeIn: 1 + r.Float64()}
			}
			e = ev(seq, TypeMapLoaded, MapLoaded{Asset: fmt.Sprintf("%064x", r.Uint64()), WidthIn: 48, HeightIn: 36, Grid: g})
		case k <= 3 || len(tokens) == 0:
			id := fmt.Sprintf("t_%d", nextToken)
			nextToken++
			tokens = append(tokens, id)
			p := pose()
			e = ev(seq, TypeTokenPlaced, TokenPlaced{TokenID: id, Label: id, Color: "#888", X: p.X, Y: p.Y, Rotation: p.Rotation, SizeIn: 1})
		case k <= 6:
			e = ev(seq, TypeTokenMoved, TokenMoved{TokenID: tokens[r.IntN(len(tokens))], To: pose()})
		case k == 7:
			i := r.IntN(len(tokens))
			e = ev(seq, TypeTokenRemoved, TokenRemoved{TokenID: tokens[i]})
			tokens = append(tokens[:i], tokens[i+1:]...)
		default:
			a, b := 1+r.IntN(20), 1+r.IntN(20)
			e = ev(seq, TypeDiceRolled, DiceRolled{Expr: "2d20", Terms: []DiceTerm{{Count: 2, Sides: 20, Results: []int{a, b}}}, Total: a + b})
		}
		if len(seats) > 0 {
			e.Seat = seats[r.IntN(len(seats))]
		}
		events = append(events, e)
	}
	return events
}

// sameState compares states field by field, not through JSON, so a field the
// snapshot encoding drops still shows up as a difference.
func sameState(a, b *State) bool { return reflect.DeepEqual(a, b) }

type fixture struct {
	db  *sql.DB
	log *eventlog.Log
}

// newFixture opens a database and appends events to it through the log.
func newFixture(t *testing.T, events []eventlog.Event) fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	reg := eventlog.NewRegistry()
	Register(reg)
	log := eventlog.New(db, reg)
	for _, e := range events {
		if _, err := log.Append(ctx, e.Session, e.Seat, "", eventlog.Draft{Type: e.Type, Data: e.Data}); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{db: db, log: log}
}

func foldPrefix(t *testing.T, events []eventlog.Event, k int) *State {
	t.Helper()
	s := New("s_1")
	mustApply(t, s, events[:k]...)
	return s
}

func TestSnapshotPlusTailEqualsFullFold(t *testing.T) {
	ctx := context.Background()
	for seed := range uint64(20) {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			events := genEvents(seed, 200)
			f := newFixture(t, events)

			full, err := Load(ctx, f.db, f.log, "s_1")
			if err != nil {
				t.Fatal(err)
			}
			if !sameState(full, foldPrefix(t, events, len(events))) {
				t.Fatal("Load from the log differs from folding the events in memory")
			}

			r := rand.New(rand.NewPCG(seed, 1))
			for _, k := range []int{1, len(events), 1 + r.IntN(len(events)), 1 + r.IntN(len(events))} {
				if _, err := f.db.Exec(`DELETE FROM snapshots`); err != nil {
					t.Fatal(err)
				}
				if err := SaveSnapshot(ctx, f.db, foldPrefix(t, events, k)); err != nil {
					t.Fatal(err)
				}
				got, err := Load(ctx, f.db, f.log, "s_1")
				if err != nil {
					t.Fatal(err)
				}
				if !sameState(got, full) {
					t.Fatalf("snapshot at seq %d plus tail differs from the full fold", k)
				}
			}
		})
	}
}

func TestLoadUsesNewestMatchingSnapshot(t *testing.T) {
	ctx := context.Background()
	events := genEvents(3, 60)
	f := newFixture(t, events)

	for _, k := range []int{10, 40, 20} {
		if err := SaveSnapshot(ctx, f.db, foldPrefix(t, events, k)); err != nil {
			t.Fatal(err)
		}
	}
	// Mark the newest snapshot so we can tell it was used: Load must start
	// from seq 40, so the marked name survives (no later session.created).
	s40 := foldPrefix(t, events, 40)
	s40.Name = "from snapshot 40"
	if err := SaveSnapshot(ctx, f.db, s40); err != nil {
		t.Fatal(err)
	}

	got, err := Load(ctx, f.db, f.log, "s_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "from snapshot 40" || got.Seq != 60 {
		t.Fatalf("name %q, seq %d; want the seq-40 snapshot folded forward to 60", got.Name, got.Seq)
	}
}

func TestLoadIgnoresUnusableSnapshots(t *testing.T) {
	ctx := context.Background()
	events := genEvents(5, 80)

	cases := map[string]string{
		"other state_v": `INSERT INTO snapshots VALUES ('s_1', 50, 999, '{"session":"s_1","seq":50,"name":"stale"}')`,
		"corrupt JSON":  `INSERT INTO snapshots VALUES ('s_1', 50, 1, '{not json')`,
		"seq mismatch":  `INSERT INTO snapshots VALUES ('s_1', 50, 1, '{"session":"s_1","seq":49,"name":"wrong"}')`,
	}
	for name, insert := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, events)
			if _, err := f.db.Exec(insert); err != nil {
				t.Fatal(err)
			}
			got, err := Load(ctx, f.db, f.log, "s_1")
			if err != nil {
				t.Fatal(err)
			}
			if !sameState(got, foldPrefix(t, events, len(events))) {
				t.Fatal("Load used an unusable snapshot instead of folding from seq 1")
			}
		})
	}
}

func TestDeletingSnapshotsLosesNothing(t *testing.T) {
	ctx := context.Background()
	events := genEvents(11, 150)
	f := newFixture(t, events)

	for _, k := range []int{50, 100, 150} {
		if err := SaveSnapshot(ctx, f.db, foldPrefix(t, events, k)); err != nil {
			t.Fatal(err)
		}
	}
	with, err := Load(ctx, f.db, f.log, "s_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM snapshots`); err != nil {
		t.Fatal(err)
	}
	without, err := Load(ctx, f.db, f.log, "s_1")
	if err != nil {
		t.Fatal(err)
	}
	if !sameState(with, without) {
		t.Fatal("state differs after deleting all snapshots")
	}
}

func TestLoadUnknownSession(t *testing.T) {
	f := newFixture(t, nil)
	if _, err := Load(context.Background(), f.db, f.log, "s_nope"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
}
