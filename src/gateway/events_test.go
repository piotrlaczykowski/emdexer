package main

import (
	"testing"
	"time"
)

// ── Event bus tests ──────────────────────────────────────────

func TestEventBus_SubscribeReceivesPublished(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	bus.publish(IndexingEvent{Namespace: "prod", Status: "complete"})

	select {
	case evt := <-ch:
		if evt.Namespace != "prod" || evt.Status != "complete" {
			t.Errorf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBus_SlowConsumerDrops(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)
	// Fill buffer (16) + overflow — must not block.
	for i := 0; i < 20; i++ {
		bus.publish(IndexingEvent{Status: "complete"})
	}
}

func TestEventBus_UnsubscribeClosesChannel(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()
	bus.unsubscribe(ch)
	// Channel should be closed — a receive returns zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel")
		}
	default:
		t.Error("expected channel to be closed, not empty")
	}
}
