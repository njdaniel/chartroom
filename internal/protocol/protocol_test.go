package protocol

import (
	"slices"
	"testing"

	"github.com/njdaniel/chartroom/internal/eventlog"
	"github.com/njdaniel/chartroom/internal/table"
)

// TestEventDataCoversTheLog checks that the client's EventData has every
// event type a client can receive: every registered type but session.created.
func TestEventDataCoversTheLog(t *testing.T) {
	reg := eventlog.NewRegistry()
	table.Register(reg)
	for _, typ := range reg.Types() {
		_, ok := EventData[typ]
		if want := typ != table.TypeSessionCreated; ok != want {
			t.Errorf("EventData has %s: %v, want %v", typ, ok, want)
		}
	}
	if len(EventData) != len(reg.Types())-1 {
		t.Errorf("EventData has types the log does not register")
	}
}

func TestMessageTypesAreDistinct(t *testing.T) {
	var all []string
	for typ := range ClientMessages {
		all = append(all, typ)
	}
	for typ := range ServerMessages {
		if slices.Contains(all, typ) {
			t.Errorf("%s is both a client and a server message", typ)
		}
	}
}
