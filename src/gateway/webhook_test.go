package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/piotrlaczykowski/emdexer/safenet"
)

// TestDispatchWebhook_Success — httptest server receives the exact JSON payload
// with correct Content-Type and User-Agent headers.
func TestDispatchWebhook_Success(t *testing.T) {
	done := make(chan []byte, 1)
	var gotHeaders http.Header

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		done <- b
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	evt := IndexedEvent{
		Event:        "namespace.indexed",
		Namespace:    "prod",
		NodeID:       "node-1",
		FilesIndexed: 42,
		Timestamp:    time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC),
	}
	dispatchWebhook(ts.Client(), ts.URL, evt)

	select {
	case body := <-done:
		var got IndexedEvent
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("invalid JSON received: %v (body=%s)", err, string(body))
		}
		if got.Event != "namespace.indexed" || got.Namespace != "prod" || got.NodeID != "node-1" || got.FilesIndexed != 42 {
			t.Errorf("payload mismatch: got %+v", got)
		}
		if ct := gotHeaders.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if ua := gotHeaders.Get("User-Agent"); ua != "emdexer-gateway/1.0" {
			t.Errorf("User-Agent = %q, want emdexer-gateway/1.0", ua)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for webhook delivery")
	}
}

// TestDispatchWebhook_5xxRetry — first response is 500, second is 200.
// Dispatcher must retry exactly once and make exactly 2 calls total.
func TestDispatchWebhook_5xxRetry(t *testing.T) {
	var count int32
	done := make(chan struct{}, 2)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&count, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		done <- struct{}{}
	}))
	defer ts.Close()

	dispatchWebhook(ts.Client(), ts.URL, IndexedEvent{Event: "namespace.indexed", Namespace: "ns"})

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for call %d (got %d calls total)", i+1, atomic.LoadInt32(&count))
		}
	}

	// No third call.
	select {
	case <-done:
		t.Fatalf("unexpected third call (count=%d)", atomic.LoadInt32(&count))
	case <-time.After(200 * time.Millisecond):
	}

	if got := atomic.LoadInt32(&count); got != 2 {
		t.Errorf("expected 2 calls (initial + 1 retry), got %d", got)
	}
}

// TestDispatchWebhook_4xxNoRetry — a 4xx response must NOT be retried.
func TestDispatchWebhook_4xxNoRetry(t *testing.T) {
	var count int32
	done := make(chan struct{}, 2) // buffer 2 so handler never blocks

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusBadRequest)
		done <- struct{}{}
	}))
	defer ts.Close()

	dispatchWebhook(ts.Client(), ts.URL, IndexedEvent{Event: "namespace.indexed"})

	// Wait for the first (and only) request to complete.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial request")
	}

	// Allow a brief window for any erroneous retry to arrive.
	select {
	case <-done:
		t.Errorf("unexpected retry on 4xx (count=%d)", atomic.LoadInt32(&count))
	case <-time.After(200 * time.Millisecond):
		// good — no retry
	}

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("expected exactly 1 call on 4xx, got %d", got)
	}
}

// TestDispatchWebhook_SSRFBlocked — safe client must refuse to dial a loopback IP.
// The httptest server handler must never be called.
func TestDispatchWebhook_SSRFBlocked(t *testing.T) {
	var hit int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	safeClient := safenet.NewSafeClient(2 * time.Second)
	dispatchWebhook(safeClient, ts.URL, IndexedEvent{Event: "namespace.indexed"})

	time.Sleep(500 * time.Millisecond)

	if got := atomic.LoadInt32(&hit); got != 0 {
		t.Errorf("SSRF guard failed: handler was called %d times", got)
	}
}

// TestDispatchWebhook_Timeout — a slow server (>5s) must not block the caller.
// dispatchWebhook must return in <500ms even when the server never responds.
func TestDispatchWebhook_Timeout(t *testing.T) {
	// handlerStarted is closed once the handler goroutine is running so we know
	// the server is holding an in-flight request before we call ts.Close().
	handlerStarted := make(chan struct{})
	var (
		wg          sync.WaitGroup
		handlerOnce sync.Once
	)
	wg.Add(1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerOnce.Do(func() {
			wg.Done()
			close(handlerStarted)
		}) // safe if called more than once
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer func() {
		ts.Close()
		// Wait for the in-flight handler goroutine to exit (r.Context() will
		// be cancelled by ts.Close, which unblocks the select above).
		wg.Wait()
	}()

	start := time.Now()
	dispatchWebhook(ts.Client(), ts.URL, IndexedEvent{Event: "namespace.indexed"})
	elapsed := time.Since(start)

	// dispatchWebhook must return immediately — it must not wait for the
	// 5s client timeout or the 10s server delay.
	if elapsed > 500*time.Millisecond {
		t.Errorf("dispatchWebhook blocked the caller for %v (want <500ms)", elapsed)
	}

	// Wait for the handler to start so ts.Close() can cancel the in-flight
	// request context and unblock wg.Wait() in the deferred cleanup above.
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		// Handler never reached — wg.Done() won't fire; decrement manually to
		// avoid a leaked goroutine blocking the test process on exit.
		wg.Done()
		t.Error("timeout waiting for handler to start")
	}
}
