package telemetry

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestInitTracer_NoEndpoint verifies that an empty endpoint is a no-op:
// InitTracer succeeds and the shutdown function is callable without panic.
func TestInitTracer_NoEndpoint(t *testing.T) {
	shutdown, err := InitTracer("test-svc", "")
	if err != nil {
		t.Fatalf("InitTracer with empty endpoint returned error: %v", err)
	}
	shutdown() // must not panic
	shutdown() // idempotent
}

// TestInitTracer_ValidEndpoint verifies that InitTracer installs a real
// TracerProvider when a non-empty endpoint is provided and that the shutdown
// function completes without error.
func TestInitTracer_ValidEndpoint(t *testing.T) {
	t.Setenv("EMDEX_OTEL_SAMPLING_RATIO", "0.5")

	// Port 14317 is unlikely to be in use; gRPC connect is lazy so this succeeds.
	shutdown, err := InitTracer("test-svc", "localhost:14317")
	if err != nil {
		t.Fatalf("InitTracer error: %v", err)
	}
	defer shutdown()

	tp := otel.GetTracerProvider()
	if tp == nil {
		t.Fatal("expected non-nil TracerProvider after InitTracer")
	}

	// Verify the provider is a real SDK provider (not the global no-op).
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "probe")
	if !span.SpanContext().IsValid() {
		t.Error("expected valid span context from SDK provider, got no-op")
	}
	span.End()
}

// TestSpanPropagation verifies W3C Trace Context round-trip:
// inject a span into HTTP headers and extract it back, confirming the
// trace ID is preserved.
func TestSpanPropagation(t *testing.T) {
	// Install an in-memory SDK tracer + W3C propagator for this test.
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		// Restore no-op tracer so tests don't bleed state.
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	})

	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	defer span.End()

	wantTraceID := span.SpanContext().TraceID()

	// Inject traceparent/tracestate into HTTP headers.
	hdr := make(http.Header)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(hdr))
	if hdr.Get("traceparent") == "" {
		t.Fatal("traceparent header not injected")
	}

	// Extract and verify the round-trip.
	extracted := otel.GetTextMapPropagator().Extract(
		context.Background(),
		propagation.HeaderCarrier(hdr),
	)
	extractedSpan := trace.SpanFromContext(extracted)
	if !extractedSpan.SpanContext().IsValid() {
		t.Fatal("extracted span context is invalid")
	}
	if got := extractedSpan.SpanContext().TraceID(); got != wantTraceID {
		t.Errorf("trace ID mismatch: got %s, want %s", got, wantTraceID)
	}
}
