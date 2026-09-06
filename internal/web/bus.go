package web

import "sync"

// Bus fans a "this id changed" notification out to every open SSE stream in this
// process.
//
// The payload is the ID and never the rendered state. Each subscriber re-reads
// the offer and renders it itself, so two events racing for the same id both
// produce the current truth — whereas pushing rendered HTML would let an older
// render arrive last and win.
//
// ⚠ There is deliberately no NATS here, and that is a departure from the house
// CQRS pattern worth stating rather than leaving to be discovered. This is one
// binary with one database file serving a small warehouse; a cross-process
// notification spine would be a dependency to run, secure and monitor in
// exchange for nothing, because there is no second process to notify. Broadcast
// is the seam where it would go: adding a publish beside the local fan-out is a
// few lines, and no handler would change, which is the property that makes
// leaving it out safe rather than short-sighted.
type Bus struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewBus returns an empty bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[string]map[chan struct{}]struct{})}
}

// Subscribe returns a channel that receives a signal whenever id changes, and a
// function that unsubscribes it.
//
// The channel is buffered with room for one and Broadcast drops rather than
// blocks, so a slow reader can never stall a writer. Dropping is free here
// precisely because the payload is an id: the reader re-reads on the next signal
// and sees everything it missed.
func (b *Bus) Subscribe(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)

	b.mu.Lock()
	if b.subs[id] == nil {
		b.subs[id] = make(map[chan struct{}]struct{})
	}
	b.subs[id][ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if set, ok := b.subs[id]; ok {
			delete(set, ch)
			// Drop the id's entry entirely once nobody is listening, so the map
			// does not grow by one key per offer ever viewed.
			if len(set) == 0 {
				delete(b.subs, id)
			}
		}
	}
}

// Broadcast signals every subscriber to id.
//
// It is called AFTER a write has been persisted, never before: publishing first
// would let the interface show a change that failed to save.
func (b *Bus) Broadcast(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subs[id] {
		select {
		case ch <- struct{}{}:
		default:
			// Already has an unread signal. A second one would tell the reader
			// nothing the first does not.
		}
	}
}
