package eventlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/njdaniel/chartroom/internal/store"
)

type moved struct {
	Token string  `json:"token"`
	X     float64 `json:"x"`
}

func testRegistry() *Registry {
	r := NewRegistry()
	r.Register("token.moved", 1, nil)
	r.Register("dice.rolled", 1, nil)
	return r
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAppendAndRead(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())
	fixed := time.Date(2026, 11, 2, 19, 4, 11, 512_000_000, time.UTC)
	log.now = func() time.Time { return fixed }

	appended, err := log.Append(ctx, "s_1", "seat_a", "i_1",
		Draft{Type: "token.moved", Data: moved{Token: "t_1", X: 1.5}},
		Draft{Type: "dice.rolled", Data: map[string]int{"total": 7}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(ctx, "s_1", "", "", Draft{Type: "dice.rolled", Data: map[string]int{"total": 3}}); err != nil {
		t.Fatal(err)
	}

	got, err := log.Read(ctx, "s_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("read %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d: seq = %d, want %d", i, e.Seq, i+1)
		}
		if e.V != EnvelopeVersion || e.Session != "s_1" || !e.At.Equal(fixed) || e.DataV != 1 {
			t.Errorf("event %d: bad envelope %+v", i, e)
		}
		if !strings.HasPrefix(e.ID, "e_") || len(e.ID) != 34 {
			t.Errorf("event %d: id = %q, want e_ + 32 hex chars", i, e.ID)
		}
	}
	if got[0].ID != appended[0].ID || got[0].Seat != "seat_a" || got[0].Cause != "i_1" {
		t.Errorf("first event does not match what was appended: %+v", got[0])
	}
	if got[2].Seat != "" || got[2].Cause != "" {
		t.Errorf("system event: seat = %q, cause = %q, want both empty", got[2].Seat, got[2].Cause)
	}

	var m moved
	if err := json.Unmarshal(got[0].Data, &m); err != nil || m != (moved{Token: "t_1", X: 1.5}) {
		t.Errorf("data = %s, err = %v", got[0].Data, err)
	}

	after, err := log.Read(ctx, "s_1", 2)
	if err != nil || len(after) != 1 || after[0].Seq != 3 {
		t.Errorf("Read(after=2) = %d events, err %v; want only seq 3", len(after), err)
	}
}

func TestSessionsHaveIndependentSeqs(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())

	for _, s := range []string{"s_a", "s_b", "s_a"} {
		if _, err := log.Append(ctx, s, "", "", Draft{Type: "dice.rolled", Data: struct{}{}}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := log.Read(ctx, "s_a", 0)
	b, _ := log.Read(ctx, "s_b", 0)
	if len(a) != 2 || a[1].Seq != 2 || len(b) != 1 || b[0].Seq != 1 {
		t.Fatalf("s_a seqs %v, s_b seqs %v; want [1 2] and [1]", seqs(a), seqs(b))
	}
}

func TestAppendIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())

	// The second draft fails to encode after the first has been inserted.
	_, err := log.Append(ctx, "s_1", "", "",
		Draft{Type: "dice.rolled", Data: struct{}{}},
		Draft{Type: "dice.rolled", Data: map[string]any{"bad": make(chan int)}},
	)
	if err == nil {
		t.Fatal("Append succeeded, want an encoding error")
	}
	if got, _ := log.Read(ctx, "s_1", 0); len(got) != 0 {
		t.Fatalf("%d events left behind after a failed batch, want 0", len(got))
	}

	// The failed batch used up no seqs.
	ok, err := log.Append(ctx, "s_1", "", "", Draft{Type: "dice.rolled", Data: struct{}{}})
	if err != nil || ok[0].Seq != 1 {
		t.Fatalf("next append: seq %v, err %v; want seq 1", seqs(ok), err)
	}
}

func TestConcurrentAppendsAreGapless(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())

	const writers, batches, perBatch = 8, 10, 3
	var wg sync.WaitGroup
	errs := make(chan error, writers*batches)
	for range writers {
		wg.Go(func() {
			for range batches {
				drafts := make([]Draft, perBatch)
				for i := range drafts {
					drafts[i] = Draft{Type: "dice.rolled", Data: struct{}{}}
				}
				got, err := log.Append(ctx, "s_1", "", "", drafts...)
				if err != nil {
					errs <- err
					return
				}
				// A batch's events are consecutive.
				for i := 1; i < len(got); i++ {
					if got[i].Seq != got[i-1].Seq+1 {
						errs <- errors.New("batch seqs not consecutive")
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	all, err := log.Read(ctx, "s_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != writers*batches*perBatch {
		t.Fatalf("read %d events, want %d", len(all), writers*batches*perBatch)
	}
	for i, e := range all {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d: gap or duplicate", i, e.Seq)
		}
	}
}

func TestAppendRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())

	cases := []struct {
		name    string
		session string
		drafts  []Draft
		want    error
	}{
		{"unknown type", "s_1", []Draft{{Type: "token.exploded", Data: struct{}{}}}, ErrUnknownType},
		{"non-object data", "s_1", []Draft{{Type: "dice.rolled", Data: 5}}, nil},
		{"nil data", "s_1", []Draft{{Type: "dice.rolled"}}, nil},
		{"empty session", "", []Draft{{Type: "dice.rolled", Data: struct{}{}}}, nil},
		{"no drafts", "s_1", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := log.Append(ctx, c.session, "", "", c.drafts...)
			if err == nil {
				t.Fatal("Append succeeded, want an error")
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	if got, _ := log.Read(ctx, "s_1", 0); len(got) != 0 {
		t.Fatalf("%d events appended by rejected calls, want 0", len(got))
	}
}

// insertRaw writes a row the way an older or newer binary might have.
func insertRaw(t *testing.T, db *sql.DB, seq int, typ string, dataV int, data string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO events (session_id, seq, id, at, type, data_v, data)
		VALUES ('s_1', ?, ?, '2026-11-02T00:00:00Z', ?, ?, ?)`,
		seq, "e_raw"+string(rune('0'+seq)), typ, dataV, data)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadRejectsUnknownTypesAndVersions(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		dataV int
		want  error
	}{
		{"unknown type", "mystery.happened", 1, ErrUnknownType},
		{"newer version", "dice.rolled", 2, ErrUnknownVersion},
		{"version zero", "dice.rolled", 0, ErrUnknownVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openDB(t)
			insertRaw(t, db, 1, "dice.rolled", 1, `{}`)
			insertRaw(t, db, 2, c.typ, c.dataV, `{}`)

			_, err := New(db, testRegistry()).Read(context.Background(), "s_1", 0)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestUpcastOnRead(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	// A binary at v1 writes {"token": "t_1", "x": 2}.
	v1 := NewRegistry()
	v1.Register("token.moved", 1, nil)
	if _, err := New(db, v1).Append(ctx, "s_1", "", "", Draft{Type: "token.moved", Data: moved{Token: "t_1", X: 2}}); err != nil {
		t.Fatal(err)
	}

	// v2 renames "token" to "token_id" and nests the position.
	v2 := NewRegistry()
	v2.Register("token.moved", 2, map[int]Upcaster{
		1: func(data json.RawMessage) (json.RawMessage, error) {
			var old moved
			if err := json.Unmarshal(data, &old); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"token_id": old.Token, "to": map[string]float64{"x": old.X}})
		},
	})
	got, err := New(db, v2).Read(ctx, "s_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].DataV != 2 || string(got[0].Data) != `{"to":{"x":2},"token_id":"t_1"}` {
		t.Fatalf("read data_v %d, data %s; want v2 shape", got[0].DataV, got[0].Data)
	}

	// The stored row is untouched.
	var stored string
	if err := db.QueryRow(`SELECT data FROM events WHERE seq = 1`).Scan(&stored); err != nil || stored != `{"token":"t_1","x":2}` {
		t.Fatalf("stored data = %s (err %v), want the original v1 payload", stored, err)
	}
}

func TestRegisterPanicsOnBadRegistration(t *testing.T) {
	up := func(d json.RawMessage) (json.RawMessage, error) { return d, nil }
	cases := map[string]func(r *Registry){
		"bad name":           func(r *Registry) { r.Register("TokenMoved", 1, nil) },
		"one part":           func(r *Registry) { r.Register("moved", 1, nil) },
		"duplicate":          func(r *Registry) { r.Register("dice.rolled", 1, nil); r.Register("dice.rolled", 1, nil) },
		"version zero":       func(r *Registry) { r.Register("dice.rolled", 0, nil) },
		"missing upcaster":   func(r *Registry) { r.Register("dice.rolled", 3, map[int]Upcaster{1: up}) },
		"wrong upcaster key": func(r *Registry) { r.Register("dice.rolled", 2, map[int]Upcaster{2: up}) },
	}
	for name, register := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()
			register(NewRegistry())
		})
	}
}

func TestEnvelopeJSON(t *testing.T) {
	e := Event{V: 1, ID: "e_1", Session: "s_1", Seq: 1, At: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		Type: "dice.rolled", DataV: 1, Data: json.RawMessage(`{}`)}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"id":"e_1","session":"s_1","seq":1,"at":"2026-11-02T00:00:00Z","seat":null,"type":"dice.rolled","data_v":1,"data":{}}`
	if string(b) != want {
		t.Fatalf("system event JSON:\n got %s\nwant %s", b, want)
	}

	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.Seat != "" {
		t.Fatalf("round trip: seat %q, err %v", back.Seat, err)
	}

	e.Seat, e.Cause = "seat_a", "i_1"
	b, _ = json.Marshal(e)
	if !strings.Contains(string(b), `"seat":"seat_a","cause":"i_1"`) {
		t.Fatalf("seat event JSON = %s", b)
	}
}

func seqs(events []Event) []int64 {
	out := make([]int64, len(events))
	for i, e := range events {
		out[i] = e.Seq
	}
	return out
}

func TestFirsts(t *testing.T) {
	ctx := context.Background()
	log := New(openDB(t), testRegistry())
	times := []time.Time{
		time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
	}
	for i, s := range []string{"s_late", "s_early"} {
		log.now = func() time.Time { return times[i] }
		for range 3 {
			if _, err := log.Append(ctx, s, "", "", Draft{Type: "dice.rolled", Data: struct{}{}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := log.Firsts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Session != "s_early" || got[1].Session != "s_late" || got[0].Seq != 1 || got[1].Seq != 1 {
		t.Fatalf("Firsts = %+v, want seq 1 of s_early then s_late", got)
	}
}
