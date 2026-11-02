// Package session runs open sessions (ADR-003). Each open session has one
// goroutine, its loop, and every change to that session goes through it in
// order:
//
//  1. authorize the intent for its seat;
//  2. validate it against current state, rolling dice if it needs them;
//  3. append the resulting events in one transaction;
//  4. apply them to the in-memory state;
//  5. hand each event to every subscriber.
//
// Because one goroutine does all of this, every subscriber sees the same
// events in the same order, and that order is the log's.
//
// A session loads from its snapshot and log on first use, and unloads (after
// writing a snapshot) once it has no subscribers and has been idle for
// Config.IdleTimeout.
package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/njdaniel/chartroom/internal/dice"
	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/protocol"
	"github.com/njdaniel/chartroom/internal/table"
)

// DefaultIdleTimeout is how long an unwatched session stays loaded.
const DefaultIdleTimeout = 5 * time.Minute

var (
	// ErrNoSession means no session has that id.
	ErrNoSession = table.ErrNoSession
	// ErrClosed means the Manager has shut down.
	ErrClosed = errors.New("session: shutting down")
)

// Config is what a Manager needs.
type Config struct {
	DB  *sql.DB // for snapshots; the same database as Log
	Log *eventlog.Log
	// Authorize is the seat permission check. Nil means AllowSeated.
	Authorize Authorizer
	// AssetExists reports whether an uploaded image has this hash. Nil
	// skips the check.
	AssetExists func(hash string) bool
	// IdleTimeout is how long a session with no subscribers stays loaded.
	// Zero means DefaultIdleTimeout.
	IdleTimeout time.Duration
}

// Subscriber receives a session's events as they are appended.
type Subscriber interface {
	// Deliver is called on the session loop for each event, in seq order,
	// with the state after that event. It must not block or keep st, and
	// must not call the Manager. Returning false unsubscribes.
	//
	// Events are not filtered: the subscriber must pass them through the
	// visibility filter before they reach a seat (invariant 4).
	Deliver(e eventlog.Event, st *table.State) bool
	// Presence is called on the session loop whenever a seat's subscriber
	// count goes to or from zero, with every seat in the session. The same
	// rules as Deliver apply.
	Presence(seats []protocol.PresenceSeat) bool
}

// Manager owns the open sessions.
type Manager struct {
	cfg     Config
	closing chan struct{}
	wg      sync.WaitGroup

	mu     sync.Mutex
	open   map[string]*loop
	closed bool
}

// NewManager returns a Manager. Call Close to unload every session.
func NewManager(cfg Config) *Manager {
	if cfg.Authorize == nil {
		cfg.Authorize = AllowSeated
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	return &Manager{cfg: cfg, closing: make(chan struct{}), open: map[string]*loop{}}
}

// ErrBadName means a session or display name is empty, too long, or has
// control characters.
var ErrBadName = errors.New("session: bad name")

// Create starts a new session called name, with a new secret dice seed, and
// returns its id.
func (m *Manager) Create(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := text("name", name, 1, MaxName); err != nil {
		return "", fmt.Errorf("%w: %s", ErrBadName, err.(*Rejection).Message)
	}
	id := newID("s_")
	_, err := m.cfg.Log.Append(ctx, id, "", "", eventlog.Draft{
		Type: table.TypeSessionCreated,
		Data: table.SessionCreated{Name: name, DiceSeed: dice.NewSeed()},
	})
	if err != nil {
		return "", fmt.Errorf("session: create: %w", err)
	}
	return id, nil
}

// Submit runs intent in from seat through session's loop. It returns the
// seqs of the events it appended, after every subscriber has been handed
// them. A *Rejection error means the intent was refused and nothing was
// appended.
func (m *Manager) Submit(ctx context.Context, session string, seat eventlog.SeatID, in protocol.Intent) ([]int64, error) {
	var seqs []int64
	var err error
	if derr := m.do(ctx, session, func(l *loop) { seqs, err = l.submit(ctx, seat, in) }); derr != nil {
		return nil, derr
	}
	return seqs, err
}

// Subscription is an attached Subscriber.
type Subscription struct {
	// Done is closed when the session unloads with this subscriber still
	// attached, which happens only when the Manager closes or the session
	// fails. Its subscriber gets no more events.
	Done <-chan struct{}
	// Detach unsubscribes. It is safe to call more than once.
	Detach func()
}

// Attach subscribes sub to session's events on behalf of seat ("" for a
// watcher with no seat). init is called first, on the loop, with the current
// state: sub then gets every event after st.Seq and none before. init must
// not keep st or call the Manager; returning an error from it cancels the
// Attach with that error.
//
// While seat has at least one subscriber it counts as online, and every
// subscriber is sent presence when that changes.
func (m *Manager) Attach(ctx context.Context, session string, seat eventlog.SeatID, sub Subscriber, init func(st *table.State) error) (Subscription, error) {
	var l *loop
	var key uint64
	var ierr error
	if err := m.do(ctx, session, func(lp *loop) {
		if init != nil {
			if ierr = init(lp.st); ierr != nil {
				return
			}
		}
		lp.nextSub++
		key = lp.nextSub
		lp.subs[key] = subscriber{seat: seat, Subscriber: sub}
		lp.online(seat, +1)
		l = lp
	}); err != nil {
		return Subscription{}, err
	}
	if ierr != nil {
		return Subscription{}, ierr
	}
	var once sync.Once
	return Subscription{
		Done: l.done,
		Detach: func() {
			once.Do(func() {
				select {
				case l.reqs <- func(l *loop) { l.remove(key) }:
				case <-l.done:
				}
			})
		},
	}, nil
}

// Close unloads every session, writing a snapshot of each, and waits for
// their loops to finish. Calls after Close return ErrClosed.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	close(m.closing)
	m.wg.Wait()
}

// do runs fn on session's loop and waits for it, loading the session if it
// is not open.
func (m *Manager) do(ctx context.Context, session string, fn func(*loop)) error {
	for {
		l, err := m.get(ctx, session)
		if err != nil {
			return err
		}
		finished := make(chan struct{})
		select {
		case l.reqs <- func(l *loop) { defer close(finished); fn(l) }:
			<-finished
			return nil
		case <-l.done:
			// It unloaded between get and now. Load it again.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// get returns session's loop, loading the session and starting its loop if
// it is not open.
func (m *Manager) get(ctx context.Context, session string) (*loop, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrClosed
	}
	if l := m.open[session]; l != nil {
		m.mu.Unlock()
		return l, nil
	}
	m.mu.Unlock()

	// Load without the lock so other sessions are not held up. If two
	// callers race, the second load is thrown away.
	st, err := table.Load(ctx, m.cfg.DB, m.cfg.Log, session)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if l := m.open[session]; l != nil {
		return l, nil
	}
	l := &loop{
		m: m, id: session, st: st, snapped: st.Seq,
		reqs: make(chan func(*loop)), done: make(chan struct{}), subs: map[uint64]subscriber{}, seats: map[eventlog.SeatID]int{},
	}
	m.open[session] = l
	m.wg.Add(1)
	go l.run()
	return l, nil
}

// unload removes l from the open sessions and closes l.done, so no new work
// reaches it. Only l's own goroutine calls it.
func (m *Manager) unload(l *loop) {
	m.mu.Lock()
	if m.open[l.id] == l {
		delete(m.open, l.id)
	}
	m.mu.Unlock()
	close(l.done)
}

// loaded reports how many sessions are open. For tests.
func (m *Manager) loaded() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.open)
}

// loop is one open session. Only its goroutine touches st and subs.
type loop struct {
	m       *Manager
	id      string
	st      *table.State
	snapped int64 // seq of the last snapshot written or loaded

	reqs    chan func(*loop) // unbuffered: a send succeeds only if the loop takes it
	done    chan struct{}
	subs    map[uint64]subscriber
	nextSub uint64
	seats   map[eventlog.SeatID]int // subscribers per seat; a seat with any is online
	failed  bool
}

func (l *loop) run() {
	defer l.m.wg.Done()
	idle := time.NewTimer(l.m.cfg.IdleTimeout)
	defer idle.Stop()
	for {
		select {
		case fn := <-l.reqs:
			fn(l)
			if l.failed {
				l.m.unload(l)
				return
			}
			idle.Reset(l.m.cfg.IdleTimeout)
		case <-idle.C:
			if len(l.subs) > 0 {
				idle.Reset(l.m.cfg.IdleTimeout)
				continue
			}
			l.m.unload(l)
			l.snapshot()
			return
		case <-l.m.closing:
			l.m.unload(l)
			l.snapshot()
			return
		}
	}
}

func (l *loop) submit(ctx context.Context, seat eventlog.SeatID, in protocol.Intent) ([]int64, error) {
	if err := l.m.cfg.Authorize(seat, in, l.st); err != nil {
		var r *Rejection
		if errors.As(err, &r) {
			return nil, r
		}
		return nil, reject(protocol.CodeForbidden, "%v", err)
	}
	drafts, err := l.plan(in)
	if err != nil {
		return nil, err
	}
	events, err := l.commit(ctx, seat, in.ID, drafts...)
	if err != nil {
		return nil, err
	}
	seqs := make([]int64, len(events))
	for i, e := range events {
		seqs[i] = e.Seq
	}
	return seqs, nil
}

// commit appends drafts, applies each event, and hands it to the
// subscribers.
func (l *loop) commit(ctx context.Context, seat eventlog.SeatID, cause string, drafts ...eventlog.Draft) ([]eventlog.Event, error) {
	events, err := l.m.cfg.Log.Append(ctx, l.id, seat, cause, drafts...)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", l.id, err)
	}
	reloaded := false
	for _, e := range events {
		if err := l.st.Apply(e); err != nil && !reloaded {
			// The events were checked against this state, so this is a bug
			// or a second writer. The log is the truth: rebuild from it,
			// which takes in the rest of these events too.
			slog.Error("session: appended event does not apply; reloading", "session", l.id, "seq", e.Seq, "err", err)
			st, lerr := table.Load(ctx, l.m.cfg.DB, l.m.cfg.Log, l.id)
			if lerr != nil {
				slog.Error("session: reload failed; unloading", "session", l.id, "err", lerr)
				l.failed = true
				return nil, fmt.Errorf("session %s: %w", l.id, err)
			}
			l.st, reloaded = st, true
		}
		for key, sub := range l.subs {
			if !sub.Deliver(e, l.st) {
				l.remove(key)
			}
		}
	}
	if l.st.Seq-l.snapped >= table.SnapshotEvery {
		l.snapshot()
	}
	return events, nil
}

type subscriber struct {
	seat eventlog.SeatID
	Subscriber
}

// remove unsubscribes key, if it is still subscribed.
func (l *loop) remove(key uint64) {
	sub, ok := l.subs[key]
	if !ok {
		return
	}
	delete(l.subs, key)
	l.online(sub.seat, -1)
}

// online changes seat's subscriber count by delta and sends presence to
// everyone if the seat came online or went offline.
func (l *loop) online(seat eventlog.SeatID, delta int) {
	if seat == "" {
		return
	}
	before := l.seats[seat] > 0
	l.seats[seat] += delta
	if l.seats[seat] <= 0 {
		delete(l.seats, seat)
	}
	if before == (l.seats[seat] > 0) {
		return
	}
	p := l.presence()
	for key, sub := range l.subs {
		if !sub.Presence(p) {
			l.remove(key) // recursion ends: each call removes a subscriber
		}
	}
}

// presence lists every seat in the session, ordered by id, with whether it
// is online. A fresh slice each time, since subscribers may keep it.
func (l *loop) presence() []protocol.PresenceSeat {
	ids := slices.Sorted(maps.Keys(l.st.Seats))
	p := make([]protocol.PresenceSeat, len(ids))
	for i, id := range ids {
		p[i] = protocol.PresenceSeat{ID: id, DisplayName: l.st.Seats[id].DisplayName, Online: l.seats[id] > 0}
	}
	return p
}

// snapshot saves the state if it has changed since the last one. A failed
// snapshot is logged, not fatal: snapshots are only a cache.
func (l *loop) snapshot() {
	if l.failed || l.st.Seq == l.snapped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := table.SaveSnapshot(ctx, l.m.cfg.DB, l.st); err != nil {
		slog.Error("session: snapshot", "session", l.id, "err", err)
		return
	}
	l.snapped = l.st.Seq
}
