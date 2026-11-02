package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/session"
	"github.com/njdaniel/chartroom/internal/table"
	"github.com/njdaniel/chartroom/internal/visibility"
)

// WebSocket defaults (ADR-003).
const (
	DefaultPingInterval = 20 * time.Second
	DefaultSendQueue    = 256 // frames waiting for a client before it counts as stuck
	helloTimeout        = 10 * time.Second
	writeTimeout        = 10 * time.Second
	maxFrameBytes       = 64 << 10
)

var (
	errSlowClient = errors.New("client is not reading")
	errGone       = errors.New("session unloaded")
)

// handleWS serves one client at GET /ws: hello, welcome, then intents in and
// filtered events out (ADR-003).
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Accept checks that the page's origin matches the host, so another
	// site open in the same browser cannot drive a seat.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has written the HTTP error
	}
	defer c.CloseNow()
	c.SetReadLimit(maxFrameBytes)

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)

	hello, ok := readHello(ctx, c)
	if !ok {
		return
	}
	seatID, token, ok := s.seatFor(ctx, c, hello)
	if !ok {
		return
	}

	conn := &wsConn{c: c, seat: seatID, filter: s.filter, out: make(chan []byte, s.sendQueue), cancel: cancel}
	sub, err := s.Sessions.Attach(ctx, hello.Session, seatID, conn, func(st *table.State) error {
		seat, ok := st.Seats[seatID]
		if !ok {
			return session.ErrUnknownSeat // a token whose seat never joined
		}
		conn.send(protocol.Welcome{
			Type: protocol.TypeWelcome, Protocol: protocol.Version,
			Seat: seat, SeatToken: token, State: s.filter.State(seatID, st), Seq: st.Seq,
		})
		return nil
	})
	if err != nil {
		closeWith(ctx, c, errorFor(err))
		return
	}
	defer sub.Detach()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		conn.writeLoop(ctx)
	}()
	go s.pingLoop(ctx, c, cancel)
	go func() {
		select {
		case <-sub.Done:
			// Say why before closing: cancelling ctx would close the
			// connection without a word.
			closeWith(context.Background(), c, protocol.Error{Code: protocol.ErrShuttingDown, Message: "the session closed; reconnect"})
			cancel(errGone)
		case <-ctx.Done():
		}
	}()

	conn.readLoop(ctx, s.Sessions, hello.Session)
	cancel(nil)
	<-writerDone
	// Anything else, such as a stuck client, is just dropped by the
	// deferred CloseNow: a polite close would wait on a client that is not
	// reading.
}

// readHello reads the first frame. On failure it has already told the
// client why and closed.
func readHello(ctx context.Context, c *websocket.Conn) (protocol.Hello, bool) {
	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	_, b, err := c.Read(hctx)
	if err != nil {
		return protocol.Hello{}, false
	}
	var h protocol.Hello
	if err := json.Unmarshal(b, &h); err != nil || h.Type != protocol.TypeHello {
		closeWith(ctx, c, protocol.Error{Code: protocol.ErrBadHello, Message: "the first message must be hello"})
		return protocol.Hello{}, false
	}
	if h.Protocol != protocol.Version {
		closeWith(ctx, c, protocol.Error{Code: protocol.ErrProtocolMismatch, Message: "this server speaks protocol 1; reload the page"})
		return protocol.Hello{}, false
	}
	return h, true
}

// seatFor finds hello's seat from its token, or makes a new seat.
func (s *server) seatFor(ctx context.Context, c *websocket.Conn, h protocol.Hello) (eventlog.SeatID, string, bool) {
	if h.SeatToken != "" {
		id, err := s.Sessions.SeatForToken(ctx, h.Session, h.SeatToken)
		if err != nil {
			closeWith(ctx, c, errorFor(err))
			return "", "", false
		}
		return id, h.SeatToken, true
	}
	seat, token, err := s.Sessions.Join(ctx, h.Session, h.DisplayName)
	if err != nil {
		closeWith(ctx, c, errorFor(err))
		return "", "", false
	}
	return seat.ID, token, true
}

func errorFor(err error) protocol.Error {
	switch {
	case errors.Is(err, session.ErrNoSession):
		return protocol.Error{Code: protocol.ErrUnknownSession, Message: "no session with that id"}
	case errors.Is(err, session.ErrUnknownSeat):
		return protocol.Error{Code: protocol.ErrUnknownSeat, Message: "that seat token is not for this session; join again without it"}
	case errors.Is(err, session.ErrBadName):
		return protocol.Error{Code: protocol.ErrBadHello, Message: err.Error()}
	case errors.Is(err, session.ErrClosed):
		return protocol.Error{Code: protocol.ErrShuttingDown, Message: "the server is shutting down"}
	default:
		slog.Error("ws: hello", "err", err)
		return protocol.Error{Code: protocol.ErrShuttingDown, Message: "server error; try again"}
	}
}

// closeWith sends an error frame and closes the connection.
func closeWith(ctx context.Context, c *websocket.Conn, e protocol.Error) {
	e.Type = protocol.TypeError
	b, _ := json.Marshal(e)
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if c.Write(wctx, websocket.MessageText, b) == nil {
		c.Close(websocket.StatusPolicyViolation, e.Code)
	}
}

// pingLoop drops a client that does not answer a ping within two intervals.
func (s *server) pingLoop(ctx context.Context, c *websocket.Conn, cancel context.CancelCauseFunc) {
	t := time.NewTicker(s.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, done := context.WithTimeout(ctx, 2*s.pingInterval)
			err := c.Ping(pctx)
			done()
			if err != nil {
				cancel(errors.New("missed pings"))
				return
			}
		}
	}
}

// wsConn is one client, and its session Subscriber. Frames for it queue in
// out; a client that lets out fill up is disconnected, so the session loop
// never waits on it.
type wsConn struct {
	c      *websocket.Conn
	seat   eventlog.SeatID
	filter visibility.Filter
	out    chan []byte
	cancel context.CancelCauseFunc
}

// Deliver sends e as this seat may see it. It runs on the session loop.
func (w *wsConn) Deliver(e eventlog.Event, st *table.State) bool {
	fe, ok := w.filter.Event(w.seat, e, st)
	if !ok {
		return true
	}
	return w.send(protocol.Event{Type: protocol.TypeEvent, Event: fe})
}

// Presence sends who is connected. It runs on the session loop.
func (w *wsConn) Presence(seats []protocol.PresenceSeat) bool {
	return w.send(protocol.Presence{Type: protocol.TypePresence, Seats: seats})
}

// send queues a frame without blocking. If the queue is full the client is
// stuck: it is disconnected, and it gets a fresh welcome when it reconnects.
func (w *wsConn) send(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("ws: encode", "err", err)
		w.cancel(err)
		return false
	}
	select {
	case w.out <- b:
		return true
	default:
		w.cancel(errSlowClient)
		return false
	}
}

func (w *wsConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-w.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := w.c.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				w.cancel(err)
				return
			}
		}
	}
}

// readLoop runs each intent through the session and answers it. It returns
// when the connection ends.
func (w *wsConn) readLoop(ctx context.Context, sessions *session.Manager, sessionID string) {
	for {
		_, b, err := w.c.Read(ctx)
		if err != nil {
			return
		}
		var in protocol.Intent
		if err := json.Unmarshal(b, &in); err != nil || in.Type != protocol.TypeIntent {
			closeWith(ctx, w.c, protocol.Error{Code: protocol.ErrBadMessage, Message: "expected an intent"})
			return
		}
		seqs, err := sessions.Submit(ctx, sessionID, w.seat, in)
		var rej *session.Rejection
		switch {
		case err == nil:
			// The events were queued for this client before Submit
			// returned, so the ack always follows them.
			w.send(protocol.Ack{Type: protocol.TypeAck, Intent: in.ID, Seqs: seqs})
		case errors.As(err, &rej):
			w.send(protocol.Rejected{Type: protocol.TypeRejected, Intent: in.ID, Code: rej.Code, Message: rej.Message})
		default:
			if ctx.Err() == nil {
				slog.Error("ws: intent", "session", sessionID, "err", err)
			}
			return
		}
	}
}
