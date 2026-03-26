package main

import "sync"

// eventBus manages SSE subscriber channels for IndexingEvents.
type eventBus struct {
	mu          sync.RWMutex
	subscribers map[chan IndexingEvent]struct{}
}

func newEventBus() *eventBus {
	return &eventBus{
		subscribers: make(map[chan IndexingEvent]struct{}),
	}
}

func (b *eventBus) subscribe() chan IndexingEvent {
	ch := make(chan IndexingEvent, 16)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *eventBus) unsubscribe(ch chan IndexingEvent) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	close(ch)
	b.mu.Unlock()
}

func (b *eventBus) publish(evt IndexingEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// Slow consumer — drop event rather than block.
		}
	}
}
