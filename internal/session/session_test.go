package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/njdaniel/chartroom/internal/dice"
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/store"
	"github.com/njdaniel/chartroom/internal/table"
)

var testAsset = strings.Repeat("ab", 32)

type fixture struct {
	m   *Manager
	log *eventlog.Log
	cfg Config
}

func newFixture(t *testing.T, edit func(*Config)) fixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := eventlog.NewRegistry()
	table.Register(reg)
	log := eventlog.New(db, reg)
	cfg := Config{DB: db, Log: log, AssetExists: func(h string) bool { return h == testAsset }}
	if edit != nil {
		edit(&cfg)
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return fixture{m: m, log: log, cfg: cfg}
}

// table creates a session with one seated GM.
func (f fixture) table(t *testing.T) (string, eventlog.SeatID) {
	t.Helper()
	ctx := context.Background()
	id, err := f.m.Create(ctx, "Friday game")
	if err != nil {
		t.Fatal(err)
	}
	seat, _, err := f.m.Join(ctx, id, "Nick")
	if err != nil {
		t.Fatal(err)
	}
	return id, seat.ID
}

func intent(id, kind string, data any) protocol.Intent {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return protocol.Intent{ID: id, Kind: kind, Data: b}
}

func (f fixture) events(t *testing.T, session string) []eventlog.Event {
	t.Helper()
	events, err := f.log.Read(context.Background(), session, 0)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func data[T any](t *testing.T, e eventlog.Event) T {
	t.Helper()
	var d T
	if err := json.Unmarshal(e.Data, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCreate(t *testing.T) {
	f := newFixture(t, nil)
	id, err := f.m.Create(context.Background(), "  Friday game  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "s_") {
		t.Errorf("session id %q", id)
	}
	events := f.events(t, id)
	if len(events) != 1 || events[0].Type != table.TypeSessionCreated || events[0].Seat != "" {
		t.Fatalf("log = %+v", events)
	}
	d := data[table.SessionCreated](t, events[0])
	if d.Name != "Friday game" || len(d.DiceSeed) != 64 {
		t.Fatalf("session.created = %+v", d)
	}

	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxName+1), "tab\there"} {
		if _, err := f.m.Create(context.Background(), bad); !errors.Is(err, ErrBadName) {
			t.Errorf("Create(%q) = %v, want ErrBadName", bad, err)
		}
	}
}

func TestJoinFirstSeatIsGM(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, err := f.m.Create(ctx, "Little Wars")
	if err != nil {
		t.Fatal(err)
	}
	gm, gmToken, err := f.m.Join(ctx, id, "Nick")
	if err != nil {
		t.Fatal(err)
	}
	player, playerToken, err := f.m.Join(ctx, id, "Sam")
	if err != nil {
		t.Fatal(err)
	}
	if gm.Role != table.RoleGM || player.Role != table.RolePlayer || gm.ID == player.ID {
		t.Fatalf("seats = %+v, %+v", gm, player)
	}

	// Each token leads back to its seat, and only in its own session.
	for token, want := range map[string]eventlog.SeatID{gmToken: gm.ID, playerToken: player.ID} {
		if got, err := f.m.SeatForToken(ctx, id, token); err != nil || got != want {
			t.Errorf("SeatForToken = %q, %v; want %q", got, err, want)
		}
	}
	other, _ := f.m.Create(ctx, "Other game")
	for session, token := range map[string]string{other: gmToken, id: "not-a-token", "": gmToken} {
		if _, err := f.m.SeatForToken(ctx, session, token); !errors.Is(err, ErrUnknownSeat) {
			t.Errorf("SeatForToken(%q, %q) = %v, want ErrUnknownSeat", session, token, err)
		}
	}
	if _, err := f.m.SeatForToken(ctx, id, ""); !errors.Is(err, ErrUnknownSeat) {
		t.Errorf("empty token = %v", err)
	}

	// Only the token's hash is stored.
	var stored int
	if err := f.cfg.DB.QueryRow(`SELECT COUNT(*) FROM seat_tokens WHERE token_hash IN (?, ?) OR seat_id = ?`, gmToken, playerToken, gmToken).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("a seat token is stored in the clear (%d rows, %v)", stored, err)
	}
	events := f.events(t, id)
	if len(events) != 3 || events[1].Seat != gm.ID || events[2].Seat != player.ID {
		t.Fatalf("log = %+v", events)
	}
	if _, _, err := f.m.Join(ctx, id, ""); !errors.Is(err, ErrBadName) {
		t.Errorf("Join with no name = %v", err)
	}
	if _, _, err := f.m.Join(ctx, "s_nope", "Nick"); !errors.Is(err, ErrNoSession) {
		t.Errorf("Join unknown session = %v", err)
	}
}

func TestIntentsProduceEvents(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, seat := f.table(t)
	submit := func(in protocol.Intent) eventlog.Event {
		t.Helper()
		seqs, err := f.m.Submit(ctx, id, seat, in)
		if err != nil {
			t.Fatalf("%s: %v", in.Kind, err)
		}
		events := f.events(t, id)
		last := events[len(events)-1]
		if len(seqs) != 1 || seqs[0] != last.Seq || last.Seat != seat || last.Cause != in.ID {
			t.Fatalf("%s: seqs %v, last event %+v", in.Kind, seqs, last)
		}
		return last
	}

	grid := &table.Grid{Kind: table.GridSquare, SizeIn: 1}
	e := submit(intent("i_1", protocol.KindMapLoad, protocol.MapLoad{Asset: testAsset, WidthIn: 48, HeightIn: 36, Grid: grid}))
	if got := data[table.MapLoaded](t, e); e.Type != table.TypeMapLoaded || got.Asset != testAsset || got.WidthIn != 48 || *got.Grid != *grid {
		t.Fatalf("map.loaded = %+v", got)
	}

	e = submit(intent("i_2", protocol.KindTokenPlace, protocol.TokenPlace{
		Label: "Orc", Color: "#AA0000", X: 3, Y: 4, Rotation: -90, SizeIn: 1,
	}))
	placed := data[table.TokenPlaced](t, e)
	if e.Type != table.TypeTokenPlaced || !strings.HasPrefix(placed.TokenID, "t_") ||
		placed.Color != "#aa0000" || placed.Rotation != 270 || placed.X != 3 {
		t.Fatalf("token.placed = %+v", placed)
	}

	e = submit(intent("i_3", protocol.KindTokenMove, protocol.TokenMove{TokenID: placed.TokenID, X: 10, Y: 5, Rotation: 720}))
	moved := data[table.TokenMoved](t, e)
	want := table.TokenMoved{TokenID: placed.TokenID, From: table.Pose{X: 3, Y: 4, Rotation: 270}, To: table.Pose{X: 10, Y: 5}}
	if e.Type != table.TypeTokenMoved || !reflect.DeepEqual(moved, want) {
		t.Fatalf("token.moved = %+v, want %+v", moved, want)
	}

	e = submit(intent("i_4", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "2d6+3", Label: "damage"}))
	rolled := data[table.DiceRolled](t, e)
	if e.Type != table.TypeDiceRolled || rolled.Expr != "2d6+3" || rolled.Label != "damage" || len(rolled.Terms) != 1 {
		t.Fatalf("dice.rolled = %+v", rolled)
	}
	// The roll is the session's first, so it is roll index 0 of its seed.
	seed := data[table.SessionCreated](t, f.events(t, id)[0]).DiceSeed
	expr, _ := dice.Parse("2d6+3")
	res, _ := dice.Roll(seed, 0, expr)
	if rolled.Total != res.Total || !reflect.DeepEqual(rolled.Terms[0].Results, res.Terms[0].Results) {
		t.Fatalf("rolled %+v, the dice source says %+v", rolled, res)
	}

	e = submit(intent("i_5", protocol.KindTokenRemove, protocol.TokenRemove{TokenID: placed.TokenID}))
	if e.Type != table.TypeTokenRemoved || data[table.TokenRemoved](t, e).TokenID != placed.TokenID {
		t.Fatalf("token.removed = %+v", e)
	}
}

func TestInvalidIntentsAreRejectedAndNotLogged(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, seat := f.table(t)
	if _, err := f.m.Submit(ctx, id, seat, intent("i_0", protocol.KindTokenPlace, protocol.TokenPlace{Color: "#000000", SizeIn: 1})); err != nil {
		t.Fatal(err)
	}
	before := len(f.events(t, id))

	place := func(edit func(*protocol.TokenPlace)) protocol.Intent {
		p := protocol.TokenPlace{Label: "Orc", Color: "#aa0000", X: 1, Y: 1, SizeIn: 1}
		edit(&p)
		return intent("i_x", protocol.KindTokenPlace, p)
	}
	raw := func(kind, data string) protocol.Intent {
		return protocol.Intent{ID: "i_x", Kind: kind, Data: json.RawMessage(data)}
	}

	for name, tc := range map[string]struct {
		in   protocol.Intent
		code string
	}{
		"unknown kind":        {raw("token.teleport", `{}`), protocol.CodeUnknownKind},
		"no intent id":        {protocol.Intent{Kind: protocol.KindDiceRoll, Data: json.RawMessage(`{"expr":"1d20"}`)}, protocol.CodeBadIntent},
		"not an object":       {raw(protocol.KindDiceRoll, `"1d20"`), protocol.CodeBadIntent},
		"misspelled field":    {raw(protocol.KindDiceRoll, `{"exp":"1d20"}`), protocol.CodeBadIntent},
		"trailing data":       {raw(protocol.KindDiceRoll, `{"expr":"1d20"}{}`), protocol.CodeBadIntent},
		"move unknown token":  {intent("i_x", protocol.KindTokenMove, protocol.TokenMove{TokenID: "t_nope"}), protocol.CodeNoToken},
		"remove unknown":      {intent("i_x", protocol.KindTokenRemove, protocol.TokenRemove{TokenID: "t_nope"}), protocol.CodeNoToken},
		"x off the table":     {place(func(p *protocol.TokenPlace) { p.X = 1e9 }), protocol.CodeBadIntent},
		"y off the table":     {place(func(p *protocol.TokenPlace) { p.Y = -MaxCoordIn - 1 }), protocol.CodeBadIntent},
		"zero size":           {place(func(p *protocol.TokenPlace) { p.SizeIn = 0 }), protocol.CodeBadIntent},
		"bad color":           {place(func(p *protocol.TokenPlace) { p.Color = "red" }), protocol.CodeBadIntent},
		"long label":          {place(func(p *protocol.TokenPlace) { p.Label = strings.Repeat("x", MaxLabel+1) }), protocol.CodeBadIntent},
		"unknown token image": {place(func(p *protocol.TokenPlace) { p.Asset = strings.Repeat("0", 64) }), protocol.CodeNoAsset},
		"bad dice":            {intent("i_x", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20 5"}), protocol.CodeBadDice},
		"no dice":             {intent("i_x", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "5"}), protocol.CodeBadDice},
		"too many dice":       {intent("i_x", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "101d6"}), protocol.CodeBadDice},
		"map not a hash":      {intent("i_x", protocol.KindMapLoad, protocol.MapLoad{Asset: "map.png", WidthIn: 1, HeightIn: 1}), protocol.CodeBadIntent},
		"map not uploaded":    {intent("i_x", protocol.KindMapLoad, protocol.MapLoad{Asset: strings.Repeat("0", 64), WidthIn: 1, HeightIn: 1}), protocol.CodeNoAsset},
		"map too wide":        {intent("i_x", protocol.KindMapLoad, protocol.MapLoad{Asset: testAsset, WidthIn: MaxMapIn + 1, HeightIn: 1}), protocol.CodeBadIntent},
		"bad grid": {intent("i_x", protocol.KindMapLoad, protocol.MapLoad{
			Asset: testAsset, WidthIn: 1, HeightIn: 1, Grid: &table.Grid{Kind: "triangle", SizeIn: 1},
		}), protocol.CodeBadIntent},
	} {
		t.Run(name, func(t *testing.T) {
			seqs, err := f.m.Submit(ctx, id, seat, tc.in)
			var r *Rejection
			if !errors.As(err, &r) || r.Code != tc.code {
				t.Fatalf("Submit = %v, %v; want rejection %s", seqs, err, tc.code)
			}
		})
	}
	if after := len(f.events(t, id)); after != before {
		t.Fatalf("rejected intents appended %d events", after-before)
	}
}

func TestAuthorizeSeesEveryIntent(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	f := newFixture(t, func(c *Config) {
		c.Authorize = func(seat eventlog.SeatID, in protocol.Intent, st *table.State) error {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, in.ID)
			return errors.New("nobody may do anything")
		}
	})
	ctx := context.Background()
	id, seat := f.table(t)
	before := len(f.events(t, id))

	ins := []protocol.Intent{
		intent("i_1", protocol.KindMapLoad, protocol.MapLoad{Asset: testAsset, WidthIn: 1, HeightIn: 1}),
		intent("i_2", protocol.KindTokenPlace, protocol.TokenPlace{Color: "#000000", SizeIn: 1}),
		intent("i_3", protocol.KindTokenMove, protocol.TokenMove{TokenID: "t_nope"}),
		intent("i_4", protocol.KindTokenRemove, protocol.TokenRemove{TokenID: "t_nope"}),
		intent("i_5", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"}),
		intent("i_6", "token.teleport", struct{}{}),
	}
	for _, in := range ins {
		_, err := f.m.Submit(ctx, id, seat, in)
		var r *Rejection
		if !errors.As(err, &r) || r.Code != protocol.CodeForbidden {
			t.Errorf("%s = %v, want forbidden", in.Kind, err)
		}
	}
	if want := []string{"i_1", "i_2", "i_3", "i_4", "i_5", "i_6"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("authorize saw %v, want %v", calls, want)
	}
	if after := len(f.events(t, id)); after != before {
		t.Errorf("denied intents appended %d events", after-before)
	}
}

func TestDefaultAuthorizeNeedsASeat(t *testing.T) {
	f := newFixture(t, nil)
	id, _ := f.table(t)
	_, err := f.m.Submit(context.Background(), id, "seat_stranger", intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"}))
	var r *Rejection
	if !errors.As(err, &r) || r.Code != protocol.CodeForbidden {
		t.Fatalf("Submit from an unknown seat = %v, want forbidden", err)
	}
}

func TestUnknownSession(t *testing.T) {
	f := newFixture(t, nil)
	_, err := f.m.Submit(context.Background(), "s_nope", "seat_x", intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"}))
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Submit = %v, want ErrNoSession", err)
	}
	if f.m.loaded() != 0 {
		t.Fatalf("a missing session stayed loaded")
	}
}

// recorder is a Subscriber that keeps every seq and presence it is handed.
type recorder struct {
	mu       sync.Mutex
	seqs     []int64
	presence [][]protocol.PresenceSeat
	stop     int64 // unsubscribe after this seq, if non-zero
}

func (r *recorder) Presence(p []protocol.PresenceSeat) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.presence = append(r.presence, p)
	return true
}

// online returns the online seats in the latest presence.
func (r *recorder) online() []eventlog.SeatID {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.presence) == 0 {
		return nil
	}
	var on []eventlog.SeatID
	for _, s := range r.presence[len(r.presence)-1] {
		if s.Online {
			on = append(on, s.ID)
		}
	}
	return on
}

func (r *recorder) Deliver(e eventlog.Event, st *table.State) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st.Seq != e.Seq {
		panic(fmt.Sprintf("delivered seq %d with state at %d", e.Seq, st.Seq))
	}
	r.seqs = append(r.seqs, e.Seq)
	return r.stop == 0 || e.Seq < r.stop
}

func (r *recorder) got() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.seqs...)
}

// TestOneOrderForEveryone has many seats submit at once while several
// subscribers watch. Each subscriber must see exactly the log's order. Run
// with -race.
func TestOneOrderForEveryone(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, gm := f.table(t)
	seats := []eventlog.SeatID{gm}
	for i := range 4 {
		s, _, err := f.m.Join(ctx, id, fmt.Sprintf("Player %d", i))
		if err != nil {
			t.Fatal(err)
		}
		seats = append(seats, s.ID)
	}
	seqs, err := f.m.Submit(ctx, id, gm, intent("i_place", protocol.KindTokenPlace, protocol.TokenPlace{Color: "#000000", SizeIn: 1}))
	if err != nil {
		t.Fatal(err)
	}
	token := data[table.TokenPlaced](t, f.events(t, id)[seqs[0]-1]).TokenID

	var start int64
	subs := make([]*recorder, 3)
	for i := range subs {
		subs[i] = &recorder{}
		sub, err := f.m.Attach(ctx, id, "", subs[i], func(st *table.State) error { start = st.Seq; return nil })
		if err != nil {
			t.Fatal(err)
		}
		defer sub.Detach()
	}

	const each = 25
	var wg sync.WaitGroup
	for i, seat := range seats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range each {
				in := intent(fmt.Sprintf("i_%d_%d", i, j), protocol.KindTokenMove, protocol.TokenMove{TokenID: token, X: float64(i), Y: float64(j)})
				if j%3 == 0 {
					in = intent(in.ID, protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"})
				}
				if _, err := f.m.Submit(ctx, id, seat, in); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	var want []int64
	for _, e := range f.events(t, id) {
		if e.Seq > start {
			want = append(want, e.Seq)
		}
	}
	if len(want) != len(seats)*each {
		t.Fatalf("log has %d new events, want %d", len(want), len(seats)*each)
	}
	for i, r := range subs {
		if got := r.got(); !reflect.DeepEqual(got, want) {
			t.Errorf("subscriber %d saw %d events in a different order from the log", i, len(got))
		}
	}

	// The dice rolls used consecutive indexes, whatever the interleaving.
	st, err := table.Load(ctx, f.cfg.DB, f.log, id)
	if err != nil {
		t.Fatal(err)
	}
	if wantRolls := int64(len(seats) * ((each + 2) / 3)); st.DiceRolls != wantRolls {
		t.Errorf("DiceRolls = %d, want %d", st.DiceRolls, wantRolls)
	}
}

func TestSubscriberCanUnsubscribe(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, seat := f.table(t)
	r := &recorder{stop: 4}
	if _, err := f.m.Attach(ctx, id, "", r, nil); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		if _, err := f.m.Submit(ctx, id, seat, intent(fmt.Sprint("i_", i), protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d6"})); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.got(); !reflect.DeepEqual(got, []int64{3, 4}) {
		t.Fatalf("subscriber saw %v, want [3 4] and then nothing", got)
	}
}

func TestPresence(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, gm := f.table(t)
	player, _, err := f.m.Join(ctx, id, "Sam")
	if err != nil {
		t.Fatal(err)
	}

	watcher := &recorder{}
	if _, err := f.m.Attach(ctx, id, "", watcher, nil); err != nil {
		t.Fatal(err)
	}
	if len(watcher.presence) != 0 {
		t.Fatal("a seatless watcher changed presence")
	}

	gmSub, err := f.m.Attach(ctx, id, gm, &recorder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := watcher.online(); !reflect.DeepEqual(got, []eventlog.SeatID{gm}) {
		t.Fatalf("online = %v, want the GM", got)
	}
	// A second tab for the same seat is not news.
	tab2, err := f.m.Attach(ctx, id, gm, &recorder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	playerSub, err := f.m.Attach(ctx, id, player.ID, &recorder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(watcher.presence); n != 2 {
		t.Fatalf("watcher got %d presence messages, want 2", n)
	}
	if got := watcher.online(); len(got) != 2 {
		t.Fatalf("online = %v, want both seats", got)
	}

	gmSub.Detach()
	if n := len(watcher.presence); n != 2 {
		t.Fatal("closing one of two tabs sent presence")
	}
	tab2.Detach()
	playerSub.Detach()
	eventually(t, "both seats to go offline", func() bool { return len(watcher.online()) == 0 })
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	last := watcher.presence[len(watcher.presence)-1]
	if len(last) != 2 || last[0].DisplayName == "" {
		t.Fatalf("presence lists %+v, want both seats with names", last)
	}
}

func TestAttachInitCanRefuse(t *testing.T) {
	f := newFixture(t, nil)
	id, seat := f.table(t)
	refused := errors.New("no such seat")
	r := &recorder{}
	if _, err := f.m.Attach(context.Background(), id, seat, r, func(*table.State) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("Attach = %v, want the init error", err)
	}
	if _, err := f.m.Submit(context.Background(), id, seat, intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d6"})); err != nil {
		t.Fatal(err)
	}
	if len(r.got()) != 0 || len(r.presence) != 0 {
		t.Fatal("a refused subscriber was still attached")
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func snapshots(t *testing.T, f fixture, session string) []int64 {
	t.Helper()
	rows, err := f.cfg.DB.Query(`SELECT seq FROM snapshots WHERE session_id = ? ORDER BY seq`, session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var s int64
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, s)
	}
	return seqs
}

func TestIdleSessionsUnloadAndReload(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.IdleTimeout = 20 * time.Millisecond })
	ctx := context.Background()
	id, seat := f.table(t)
	roll := func(n int) table.DiceRolled {
		t.Helper()
		seqs, err := f.m.Submit(ctx, id, seat, intent(fmt.Sprint("i_", n), protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d1000"}))
		if err != nil {
			t.Fatal(err)
		}
		return data[table.DiceRolled](t, f.events(t, id)[seqs[0]-1])
	}

	first := roll(0)
	eventually(t, "the idle session to unload", func() bool { return f.m.loaded() == 0 })
	if got := snapshots(t, f, id); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("snapshots after unload at seq %v, want [3]", got)
	}

	// It loads again on use, and the dice carry on from roll 1, not 0.
	second := roll(1)
	seed := data[table.SessionCreated](t, f.events(t, id)[0]).DiceSeed
	expr, _ := dice.Parse("1d1000")
	want, _ := dice.Roll(seed, 1, expr)
	if second.Total != want.Total {
		t.Fatalf("second roll = %d, want roll index 1's %d (first was %d)", second.Total, want.Total, first.Total)
	}
}

func TestWatchedSessionsStayLoaded(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.IdleTimeout = 10 * time.Millisecond })
	id, _ := f.table(t)
	sub, err := f.m.Attach(context.Background(), id, "", &recorder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if f.m.loaded() != 1 {
		t.Fatal("a session with a subscriber unloaded")
	}
	sub.Detach()
	sub.Detach() // twice is fine
	eventually(t, "the session to unload after Detach", func() bool { return f.m.loaded() == 0 })
}

func TestClose(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	id, seat := f.table(t)
	sub, err := f.m.Attach(ctx, id, "", &recorder{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.m.Close()

	select {
	case <-sub.Done:
	default:
		t.Fatal("subscription not done after Close")
	}
	sub.Detach() // must not block
	if got := snapshots(t, f, id); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("snapshots after Close at %v, want [2]", got)
	}
	if _, err := f.m.Submit(ctx, id, seat, intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"})); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit after Close = %v, want ErrClosed", err)
	}
	f.m.Close() // twice is fine
}

func TestRotation(t *testing.T) {
	for in, want := range map[float64]float64{0: 0, 90: 90, 360: 0, -90: 270, 725: 5, -1e-20: 0, -360: 0} {
		got, err := rotation(in)
		if err != nil || got != want {
			t.Errorf("rotation(%g) = %g, %v; want %g", in, got, err, want)
		}
	}
}
