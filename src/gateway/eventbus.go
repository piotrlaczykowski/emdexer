package main

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	sseSubscribers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "emdexer_gateway_sse_subscribers",
		Help: "Number of active SSE subscribers on /v1/events/indexing",
	})
	sseEventsPublished = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_gateway_sse_events_published_total",
		Help: "Total indexing events published to the SSE bus",
	})
	sseEventsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_gateway_sse_events_dropped_total",
		Help: "Total indexing events dropped due to slow SSE consumers",
	})
)

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
	sseSubscribers.Inc()
	return ch
}

func (b *eventBus) unsubscribe(ch chan IndexingEvent) {
	b.mu.Lock()
	delete(b.subscribers, ch)
	close(ch)
	b.mu.Unlock()
	sseSubscribers.Dec()
}

func (b *eventBus) publish(evt IndexingEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sseEventsPublished.Inc()
	for ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// Slow consumer — drop event rather than block.
			sseEventsDropped.Inc()
		}
	}
}
