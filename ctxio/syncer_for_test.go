package ctxio

import "testing"

// syncer simulates a set of concurrent events that actually take place
// in a specific, pre-defined sequence determined by a test. It's meant to
// test thread-safety.
//
// This is somewhat abstract since I hope to pull this out into a library
// some day.
type syncer struct {
	t       *testing.T
	events  []syncEvent
	actorCs []chan syncEvent
}

type syncEvent struct {
	actor int
	value any
}

// newSyncer creates a syncer with a sequence of events. Each event has an
// assigned actor who is supposed to perform the event by calling sync,
// and a value that is returned by sync.
func newSyncer(t *testing.T, events []syncEvent) *syncer {
	maxActor := 0
	for _, e := range events {
		if e.actor > maxActor {
			maxActor = e.actor
		}
	}
	actorCs := make([]chan syncEvent, maxActor+1)
	for i := range actorCs {
		actorCs[i] = make(chan syncEvent)
	}
	syn := &syncer{
		t:       t,
		events:  events,
		actorCs: actorCs,
	}

	doneC := make(chan struct{})
	go func() {
		for _, e := range events {
			select {
			case <-doneC:
				return
			case syn.actorCs[e.actor] <- e:
			}
		}
	}()
	t.Cleanup(func() { close(doneC) })

	return syn
}

// sync is called by an actor in a thread-safety test. sync blocks until
// the next event belongs to the given actor (other actors must go first).
// When this condition is satisfied, sync returns the value associated with
// the event. sync reports a fatal error if there are no more events.
func (s *syncer) sync(actor int) any {
	return (<-s.actorCs[actor]).value
}
