package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

func TestResponseWriter_ImplementsFlusher(t *testing.T) {
	base := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	rw := &responseWriter{ResponseWriter: base, status: http.StatusOK}

	// Verify Flusher interface is satisfied
	flusher, ok := any(rw).(http.Flusher)
	if !ok {
		t.Fatal("responseWriter does not implement http.Flusher")
	}
	flusher.Flush()
	if !base.flushed {
		t.Error("Flush() was not forwarded to underlying ResponseWriter")
	}
}
