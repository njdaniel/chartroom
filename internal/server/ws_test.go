package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/session"
	"github.com/njdaniel/chartroom/internal/store"
	"github.com/njdaniel/chartroom/internal/table"
)

type wsFixture struct {
	url      string // ws://…/ws
	log      *eventlog.Log
	sessions *session.Manager
	session  string
}

// newWSFixture starts a server with one session. edit may change the
// server config; listen may wrap the listener.
func newWSFixture(t *testing.T, edit func(*Config), listen func(net.Listener) net.Listener) wsFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "chartroom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := eventlog.NewRegistry()
	table.Register(reg)
	log := eventlog.New(db, reg)
	sessions := session.NewManager(session.Config{DB: db, Log: log})
	t.Cleanup(sessions.Close)

	cfg := Config{Log: log, Sessions: sessions, AssetsDir: dir}
	if edit != nil {
		edit(&cfg)
	}
	srv := httptest.NewUnstartedServer(New(cfg))
	if listen != nil {
		srv.Listener = listen(srv.Listener)
	}
	srv.Start()
	t.Cleanup(srv.Close)

	id, err := sessions.Create(ctx, "Friday game")
	if err != nil {
		t.Fatal(err)
	}
	return wsFixture{url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws", log: log, sessions: sessions, session: id}
}

func (f wsFixture) seed(t *testing.T) string {
	t.Helper()
	events, err := f.log.Read(context.Background(), f.session, 0)
	if err != nil {
		t.Fatal(err)
	}
	var d table.SessionCreated
	if err := json.Unmarshal(events[0].Data, &d); err != nil {
		t.Fatal(err)
	}
	return d.DiceSeed
}

// wsClient is a test client. It keeps every frame it reads.
type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []string
	online map[eventlog.SeatID]bool // from the latest presence read
}

func dialWS(t *testing.T, url string, opts *websocket.DialOptions) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c}
}

func (c *wsClient) send(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.c.Write(ctx, websocket.MessageText, b); err != nil {
		c.t.Fatal(err)
	}
}

// read returns the next frame's type and bytes, or an error once the server
// has closed the connection.
func (c *wsClient) read() (string, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.c.Read(ctx)
	if err != nil {
		return "", nil, err
	}
	c.mu.Lock()
	c.frames = append(c.frames, string(b))
	c.mu.Unlock()
	var m struct {
		Type  string
		Seats []protocol.PresenceSeat
	}
	if err := json.Unmarshal(b, &m); err != nil {
		c.t.Fatalf("frame is not JSON: %s", b)
	}
	if m.Type == protocol.TypePresence {
		c.mu.Lock()
		c.online = map[eventlog.SeatID]bool{}
		for _, s := range m.Seats {
			c.online[s.ID] = s.Online
		}
		c.mu.Unlock()
	}
	return m.Type, b, nil
}

// expect reads frames until one of type typ, skipping presence (unless typ
// is presence), and decodes it into v. Any other type fails the test.
func (c *wsClient) expect(typ string, v any) {
	c.t.Helper()
	for {
		got, b, err := c.read()
		if err != nil {
			c.t.Fatalf("waiting for %s: %v", typ, err)
		}
		if got == protocol.TypePresence && typ != protocol.TypePresence {
			continue
		}
		if got != typ {
			c.t.Fatalf("got %s, want %s: %s", got, typ, b)
		}
		if v != nil {
			if err := json.Unmarshal(b, v); err != nil {
				c.t.Fatal(err)
			}
		}
		return
	}
}

// expectClosed reads until the server closes, failing on anything but
// presence.
func (c *wsClient) expectClosed() {
	c.t.Helper()
	for {
		typ, b, err := c.read()
		if err != nil {
			return
		}
		if typ != protocol.TypePresence {
			c.t.Fatalf("want the connection closed, got %s", b)
		}
	}
}

// ackFor reads until the ack or rejection for intent id, skipping events
// and presence.
func (c *wsClient) ackFor(id string) protocol.Ack {
	c.t.Helper()
	for {
		typ, b, err := c.read()
		if err != nil {
			c.t.Fatalf("waiting for the ack of %s: %v", id, err)
		}
		switch typ {
		case protocol.TypeEvent, protocol.TypePresence:
			continue
		case protocol.TypeAck:
			var ack protocol.Ack
			_ = json.Unmarshal(b, &ack)
			if ack.Intent == id {
				return ack
			}
		}
		c.t.Fatalf("waiting for the ack of %s, got %s", id, b)
	}
}

// eventOf reads until an event of type typ, skipping presence and other
// events.
func (c *wsClient) eventOf(typ string) eventlog.Event {
	c.t.Helper()
	for {
		var ev protocol.Event
		c.expect(protocol.TypeEvent, &ev)
		if ev.Event.Type == typ {
			return ev.Event
		}
	}
}

func (c *wsClient) hello(session, token, name string) protocol.Welcome {
	c.t.Helper()
	c.send(protocol.Hello{Type: protocol.TypeHello, Protocol: protocol.Version, Session: session, SeatToken: token, DisplayName: name})
	var w protocol.Welcome
	c.expect(protocol.TypeWelcome, &w)
	return w
}

func (c *wsClient) intent(id, kind string, data any) {
	c.t.Helper()
	b, _ := json.Marshal(data)
	c.send(protocol.Intent{Type: protocol.TypeIntent, ID: id, Kind: kind, Data: b})
}

func (c *wsClient) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.frames, "\n")
}

func TestWSJoinAndReconnect(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	gm := dialWS(t, f.url, nil).hello(f.session, "", "Nick")
	player := dialWS(t, f.url, nil).hello(f.session, "", "Sam")

	if gm.Seat.Role != table.RoleGM || player.Seat.Role != table.RolePlayer || gm.Seat.ID == player.Seat.ID {
		t.Fatalf("seats %+v and %+v", gm.Seat, player.Seat)
	}
	if gm.SeatToken == "" || gm.SeatToken == player.SeatToken || gm.Protocol != protocol.Version {
		t.Fatalf("tokens %q and %q", gm.SeatToken, player.SeatToken)
	}
	if player.Seq != 3 || player.State.Seq != 3 || len(player.State.Seats) != 2 || player.State.Name != "Friday game" {
		t.Fatalf("player's welcome state = %+v at seq %d", player.State, player.Seq)
	}

	// Coming back with the token gives the same seat, and no new seat.joined.
	again := dialWS(t, f.url, nil).hello(f.session, gm.SeatToken, "ignored")
	if again.Seat != gm.Seat || again.SeatToken != gm.SeatToken {
		t.Fatalf("reconnect gave %+v, want %+v", again.Seat, gm.Seat)
	}
	events, _ := f.log.Read(context.Background(), f.session, 0)
	if len(events) != 3 {
		t.Fatalf("log has %d events after a reconnect, want 3", len(events))
	}
}

func TestWSHelloErrors(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	other, _ := f.sessions.Create(context.Background(), "Other game")
	otherToken := dialWS(t, f.url, nil).hello(other, "", "Nick").SeatToken

	for name, tc := range map[string]struct {
		hello any
		code  string
	}{
		"old protocol":       {protocol.Hello{Type: protocol.TypeHello, Protocol: 0, Session: f.session, DisplayName: "Nick"}, protocol.ErrProtocolMismatch},
		"newer protocol":     {protocol.Hello{Type: protocol.TypeHello, Protocol: 2, Session: f.session, DisplayName: "Nick"}, protocol.ErrProtocolMismatch},
		"not a hello":        {protocol.Intent{Type: protocol.TypeIntent, ID: "i_1", Kind: protocol.KindDiceRoll}, protocol.ErrBadHello},
		"not JSON":           {"hello", protocol.ErrBadHello},
		"unknown session":    {protocol.Hello{Type: protocol.TypeHello, Protocol: 1, Session: "s_nope", DisplayName: "Nick"}, protocol.ErrUnknownSession},
		"made-up token":      {protocol.Hello{Type: protocol.TypeHello, Protocol: 1, Session: f.session, SeatToken: "abc"}, protocol.ErrUnknownSeat},
		"other game's token": {protocol.Hello{Type: protocol.TypeHello, Protocol: 1, Session: f.session, SeatToken: otherToken}, protocol.ErrUnknownSeat},
		"no display name":    {protocol.Hello{Type: protocol.TypeHello, Protocol: 1, Session: f.session}, protocol.ErrBadHello},
	} {
		t.Run(name, func(t *testing.T) {
			c := dialWS(t, f.url, nil)
			if s, ok := tc.hello.(string); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = c.c.Write(ctx, websocket.MessageText, []byte(s))
			} else {
				c.send(tc.hello)
			}
			var e protocol.Error
			c.expect(protocol.TypeError, &e)
			if e.Code != tc.code {
				t.Fatalf("error %+v, want code %s", e, tc.code)
			}
			if _, _, err := c.read(); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("after the error: %v, want a policy-violation close", err)
			}
		})
	}
}

func TestWSRejectsOtherOrigins(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, f.url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial = %v, %v; want 403", resp, err)
	}
}

func TestWSIntents(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	a := dialWS(t, f.url, nil)
	a.hello(f.session, "", "Nick")
	b := dialWS(t, f.url, nil)
	b.hello(f.session, "", "Sam")

	// a was already here, so it sees b join.
	if e := a.eventOf(table.TypeSeatJoined); e.Seq != 3 {
		t.Fatalf("seat.joined at %d", e.Seq)
	}

	a.intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "2d6+1", Label: "attack"})

	// The sender gets the event, then the ack.
	var ev protocol.Event
	a.expect(protocol.TypeEvent, &ev)
	var ack protocol.Ack
	a.expect(protocol.TypeAck, &ack)
	if ev.Event.Type != table.TypeDiceRolled || ev.Event.Cause != "i_1" || ack.Intent != "i_1" || len(ack.Seqs) != 1 || ack.Seqs[0] != ev.Event.Seq {
		t.Fatalf("event %+v, ack %+v", ev.Event, ack)
	}
	// Everyone else gets the same event.
	if e := b.eventOf(table.TypeDiceRolled); e.ID != ev.Event.ID {
		t.Fatalf("b saw %+v, a saw %+v", e, ev.Event)
	}

	a.intent("i_2", protocol.KindTokenMove, protocol.TokenMove{TokenID: "t_nope"})
	var rej protocol.Rejected
	a.expect(protocol.TypeRejected, &rej)
	if rej.Intent != "i_2" || rej.Code != protocol.CodeNoToken || rej.Message == "" {
		t.Fatalf("rejected = %+v", rej)
	}

	a.send(protocol.Hello{Type: protocol.TypeHello, Protocol: 1})
	var e protocol.Error
	a.expect(protocol.TypeError, &e)
	if e.Code != protocol.ErrBadMessage {
		t.Fatalf("second hello: %+v, want bad_message", e)
	}
	a.expectClosed()
}

func TestWSNeverSendsTheSeed(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	seed := f.seed(t)
	a := dialWS(t, f.url, nil)
	w := a.hello(f.session, "", "Nick")
	b := dialWS(t, f.url, nil)
	b.hello(f.session, "", "Sam")

	a.intent("i_1", protocol.KindTokenPlace, protocol.TokenPlace{Color: "#000000", SizeIn: 1})
	a.intent("i_2", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"})
	a.ackFor("i_1")
	a.ackFor("i_2")
	b.eventOf(table.TypeTokenPlaced)
	b.eventOf(table.TypeDiceRolled)
	// A reconnect gets a fresh welcome too.
	c := dialWS(t, f.url, nil)
	c.hello(f.session, w.SeatToken, "")

	for name, cl := range map[string]*wsClient{"a": a, "b": b, "reconnect": c} {
		if all := cl.all(); strings.Contains(all, seed) || strings.Contains(all, "dice_seed") {
			t.Errorf("client %s was sent the dice seed", name)
		}
	}
}

// hideAll is a filter that lets nothing through.
type hideAll struct{}

func (hideAll) Event(eventlog.SeatID, eventlog.Event, *table.State) (eventlog.Event, bool) {
	return eventlog.Event{}, false
}

func (hideAll) State(eventlog.SeatID, *table.State) protocol.State {
	return protocol.State{Name: "hidden"}
}

func TestWSEverythingGoesThroughTheFilter(t *testing.T) {
	f := newWSFixture(t, func(c *Config) { c.Filter = hideAll{} }, nil)
	a := dialWS(t, f.url, nil)
	w := a.hello(f.session, "", "Nick")
	if w.State.Name != "hidden" || w.State.Seats != nil || w.State.Seq != 0 {
		t.Fatalf("welcome state = %+v, want only what the filter returned", w.State)
	}

	a.intent("i_1", protocol.KindDiceRoll, protocol.DiceRoll{Expr: "1d20"})
	a.intent("i_2", protocol.KindTokenPlace, protocol.TokenPlace{Color: "#000000", SizeIn: 1})
	// Acks still come, but no events: the next non-presence frames are acks.
	for _, id := range []string{"i_1", "i_2"} {
		var ack protocol.Ack
		a.expect(protocol.TypeAck, &ack)
		if ack.Intent != id {
			t.Fatalf("ack %+v, want %s", ack, id)
		}
	}
}

// smallBuffers makes the kernel hold only a few KB for each accepted
// connection, so a client that stops reading backs up quickly.
type smallBuffers struct{ net.Listener }

func (l smallBuffers) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetWriteBuffer(4096)
	}
	return c, err
}

func smallReadBuffer() *websocket.DialOptions {
	return &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if tc, ok := c.(*net.TCPConn); ok {
				_ = tc.SetReadBuffer(4096)
			}
			return c, err
		},
	}}}
}

// waitOffline reads frames on c until presence says seat is offline.
func waitOffline(t *testing.T, c *wsClient, seat eventlog.SeatID) {
	t.Helper()
	for {
		c.mu.Lock()
		on, known := c.online[seat]
		c.mu.Unlock()
		if known && !on {
			return
		}
		if _, _, err := c.read(); err != nil {
			t.Fatalf("waiting for %s to go offline: %v", seat, err)
		}
	}
}

func TestWSStuckClientIsDropped(t *testing.T) {
	f := newWSFixture(t, func(c *Config) { c.SendQueue = 8 }, func(l net.Listener) net.Listener { return smallBuffers{l} })
	stuck := dialWS(t, f.url, smallReadBuffer())
	stuckSeat := stuck.hello(f.session, "", "Asleep").Seat.ID
	// stuck reads nothing from here on.

	good := dialWS(t, f.url, nil)
	good.hello(f.session, "", "Sam")
	start := time.Now()
	const rolls = 300
	for i := range rolls {
		id := fmt.Sprint("i_", i)
		good.intent(id, protocol.KindDiceRoll, protocol.DiceRoll{Expr: "100d1000"})
		good.ackFor(id)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("%d rolls took %s: the stuck client held up the session", rolls, d)
	}

	// The server dropped it: its seat went offline, and the rest of its
	// stream ends early.
	waitOffline(t, good, stuckSeat)
	events := 0
	for {
		typ, _, err := stuck.read()
		if err != nil {
			break
		}
		if typ == protocol.TypeEvent {
			events++
		}
	}
	if events >= rolls {
		t.Fatalf("stuck client got all %d events; it should have been cut off", events)
	}
}

func TestWSMissedPingsDisconnect(t *testing.T) {
	f := newWSFixture(t, func(c *Config) { c.PingInterval = 30 * time.Millisecond }, nil)
	quiet := dialWS(t, f.url, nil)
	quietSeat := quiet.hello(f.session, "", "Quiet").Seat.ID
	// quiet stops reading, so it never answers a ping.

	watcher := dialWS(t, f.url, nil)
	watcher.hello(f.session, "", "Sam")
	// watcher keeps reading (inside waitOffline), so it answers its pings
	// and stays connected.
	waitOffline(t, watcher, quietSeat)
}

func TestWSShutdown(t *testing.T) {
	f := newWSFixture(t, nil, nil)
	c := dialWS(t, f.url, nil)
	c.hello(f.session, "", "Nick")
	f.sessions.Close()
	var e protocol.Error
	c.expect(protocol.TypeError, &e)
	if e.Code != protocol.ErrShuttingDown {
		t.Fatalf("error = %+v", e)
	}

	late := dialWS(t, f.url, nil)
	late.send(protocol.Hello{Type: protocol.TypeHello, Protocol: 1, Session: f.session, DisplayName: "Late"})
	late.expect(protocol.TypeError, &e)
	if e.Code != protocol.ErrShuttingDown {
		t.Fatalf("hello after shutdown: %+v", e)
	}
}
