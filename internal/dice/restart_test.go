package dice_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/njdaniel/chartroom/internal/dice"
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/store"
	"github.com/njdaniel/chartroom/internal/table"
)

var exprs = []string{"1d20+5", "2d6", "d4", "3d8-2", "1d100", "4d6", "1d20", "2d10+1d4+3"}

func toEvent(r dice.Result) table.DiceRolled {
	d := table.DiceRolled{Expr: r.Expr, Modifier: r.Modifier, Total: r.Total}
	for _, t := range r.Terms {
		d.Terms = append(d.Terms, table.DiceTerm{Count: t.Count, Sides: t.Sides, Results: t.Results})
	}
	return d
}

// After a restart, the next roll must match what an uninterrupted server
// would have rolled: whether the session loads from the log alone or from a
// snapshot plus the log.
func TestRestartContinuesTheSameSequence(t *testing.T) {
	const rolls = 40
	seed := dice.NewSeed()

	var uninterrupted []dice.Result
	for i := range rolls {
		e, err := dice.Parse(exprs[i%len(exprs)])
		if err != nil {
			t.Fatal(err)
		}
		r, err := dice.Roll(seed, int64(i), e)
		if err != nil {
			t.Fatal(err)
		}
		uninterrupted = append(uninterrupted, r)
	}

	for _, restartAt := range []int{0, 1, 17, rolls - 1} {
		for _, snapshot := range []bool{false, true} {
			t.Run(fmt.Sprintf("restart after %d rolls, snapshot %v", restartAt, snapshot), func(t *testing.T) {
				got := runWithRestart(t, seed, rolls, restartAt, snapshot)
				if !reflect.DeepEqual(got, uninterrupted) {
					t.Fatalf("rolls differ from an uninterrupted server:\n got %v\nwant %v", got, uninterrupted)
				}
			})
		}
	}
}

// runWithRestart rolls through a real log, closing and reopening the
// database after restartAt rolls, and returns every result.
func runWithRestart(t *testing.T, seed string, rolls, restartAt int, snapshot bool) []dice.Result {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chartroom.db")

	open := func() (*eventlog.Log, func() (*table.State, error), func()) {
		db, err := store.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		reg := eventlog.NewRegistry()
		table.Register(reg)
		log := eventlog.New(db, reg)
		load := func() (*table.State, error) { return table.Load(ctx, db, log, "s_1") }
		if snapshot {
			load = func() (*table.State, error) {
				s, err := table.Load(ctx, db, log, "s_1")
				if err == nil {
					err = table.SaveSnapshot(ctx, db, s)
				}
				return s, err
			}
		}
		return log, load, func() { _ = db.Close() }
	}

	log, load, closeDB := open()
	if _, err := log.Append(ctx, "s_1", "", "", eventlog.Draft{
		Type: table.TypeSessionCreated, Data: table.SessionCreated{Name: "restart", DiceSeed: seed},
	}); err != nil {
		t.Fatal(err)
	}

	var results []dice.Result
	for i := range rolls {
		if i == restartAt {
			closeDB()
			log, load, closeDB = open()
		}
		s, err := load()
		if err != nil {
			t.Fatal(err)
		}
		e, err := dice.Parse(exprs[i%len(exprs)])
		if err != nil {
			t.Fatal(err)
		}
		r, err := dice.Roll(s.DiceSeed, s.DiceRolls, e)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, r)

		// Interleave other events so the roll index counts rolls, not events.
		drafts := []eventlog.Draft{{Type: table.TypeDiceRolled, Data: toEvent(r)}}
		if i%3 == 0 {
			drafts = append(drafts, eventlog.Draft{Type: table.TypeTokenPlaced, Data: table.TokenPlaced{TokenID: fmt.Sprintf("t_%d", i)}})
		}
		if _, err := log.Append(ctx, "s_1", "", "", drafts...); err != nil {
			t.Fatal(err)
		}
	}
	closeDB()
	return results
}
