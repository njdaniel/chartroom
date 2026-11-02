package eventlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
)

var (
	// ErrUnknownType means an event's type is not registered. Reading such
	// an event is a hard error: skipping it would build the wrong state.
	ErrUnknownType = errors.New("eventlog: unknown event type")
	// ErrUnknownVersion means an event's data_v is outside 1..latest for
	// its type.
	ErrUnknownVersion = errors.New("eventlog: unknown event data version")
)

// Upcaster converts one event type's data from version v to v+1. It must be
// pure: same input, same output, no I/O.
type Upcaster func(data json.RawMessage) (json.RawMessage, error)

// typeName is ADR-002's naming rule: lowercase, dot-separated, at least two parts.
var typeName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

type typeInfo struct {
	latest    int
	upcasters map[int]Upcaster // upcasters[v] turns v into v+1
}

// Registry lists the event types a Log accepts, each with its latest data
// version and the upcasters that bring older versions up to it.
type Registry struct {
	types map[string]typeInfo
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{types: map[string]typeInfo{}}
}

// Register adds an event type whose current data version is latest.
// upcasters must hold an entry for every version from 1 to latest-1.
// Register panics on a bad registration: it is called at startup, and a
// misregistered type must stop the server rather than misread a log.
func (r *Registry) Register(typ string, latest int, upcasters map[int]Upcaster) {
	if !typeName.MatchString(typ) {
		panic(fmt.Sprintf("eventlog: invalid event type name %q", typ))
	}
	if _, dup := r.types[typ]; dup {
		panic(fmt.Sprintf("eventlog: event type %q registered twice", typ))
	}
	if latest < 1 {
		panic(fmt.Sprintf("eventlog: %s: latest version must be at least 1, got %d", typ, latest))
	}
	if len(upcasters) != latest-1 {
		panic(fmt.Sprintf("eventlog: %s: want %d upcasters for versions 1..%d, got %d", typ, latest-1, latest-1, len(upcasters)))
	}
	for v := 1; v < latest; v++ {
		if upcasters[v] == nil {
			panic(fmt.Sprintf("eventlog: %s: missing upcaster from version %d", typ, v))
		}
	}
	r.types[typ] = typeInfo{latest: latest, upcasters: upcasters}
}

// latest returns the current data version of typ.
// Types returns the registered event types, sorted.
func (r *Registry) Types() []string {
	return slices.Sorted(maps.Keys(r.types))
}

func (r *Registry) latest(typ string) (int, error) {
	info, ok := r.types[typ]
	if !ok {
		return 0, fmt.Errorf("%w %q", ErrUnknownType, typ)
	}
	return info.latest, nil
}

// upcast brings e's data to the latest version of its type, in place.
func (r *Registry) upcast(e *Event) error {
	info, ok := r.types[e.Type]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownType, e.Type)
	}
	if e.DataV < 1 || e.DataV > info.latest {
		return fmt.Errorf("%w: %s version %d (latest is %d)", ErrUnknownVersion, e.Type, e.DataV, info.latest)
	}
	for e.DataV < info.latest {
		data, err := info.upcasters[e.DataV](e.Data)
		if err != nil {
			return fmt.Errorf("eventlog: upcast %s from version %d: %w", e.Type, e.DataV, err)
		}
		e.Data = data
		e.DataV++
	}
	return nil
}
