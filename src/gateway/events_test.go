package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// ── Handler tests ────────────────────────────────────────────

func TestHandleNodeIndexed_PublishesEvent(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	srv := &Server{events: bus}

	body := `{"namespace":"prod","files_indexed":10,"files_skipped":1,"status":"complete"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/node-1/indexed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleNodeIndexed(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	select {
	case evt := <-ch:
		if evt.Namespace != "prod" || evt.FilesIndexed != 10 || evt.NodeID != "node-1" {
			t.Errorf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestHandleNodeIndexed_MethodNotAllowed(t *testing.T) {
	srv := &Server{events: newEventBus()}
	req := httptest.NewRequest(http.MethodGet, "/v1/nodes/node-1/indexed", nil)
	w := httptest.NewRecorder()
	srv.handleNodeIndexed(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleNodeIndexed_DefaultStatus(t *testing.T) {
	bus := newEventBus()
	ch := bus.subscribe()
	defer bus.unsubscribe(ch)

	srv := &Server{events: bus}
	body := `{"namespace":"ns","files_indexed":5,"files_skipped":0}`
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/node-x/indexed", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleNodeIndexed(w, req)

	select {
	case evt := <-ch:
		if evt.Status != "complete" {
			t.Errorf("expected default status 'complete', got %q", evt.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
