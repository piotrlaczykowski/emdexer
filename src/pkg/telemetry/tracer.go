package telemetry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/piotrlaczykowski/emdexer/version"
)

// InitTracer initialises the global OpenTelemetry TracerProvider with an OTLP/gRPC exporter.
//
//   - serviceName overrides the service.name resource attribute.
//   - endpoint is the OTLP/gRPC collector address (e.g. "otel-collector:4317").
//
// When endpoint is empty the function is a no-op: the default no-op tracer
// provider remains installed and zero overhead is incurred.
//
// The returned shutdown function must be called on process exit to flush any
// pending spans. It is safe to call multiple times.
func InitTracer(serviceName, endpoint string) (func(), error) {
	if endpoint == "" {
		return func() {}, nil
	}

	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: dial %q: %w", endpoint, err)
	}

	exp, err := otlptracegrpc.New(context.Background(), otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("telemetry: create OTLP exporter: %w", err)
	}

	namespace := os.Getenv("EMDEX_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version.Version),
			semconv.ServiceNamespace(namespace),
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(parseSamplingRatio())),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var stopped bool
	return func() {
		if stopped {
			return
		}
		stopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
		_ = conn.Close()
	}, nil
}

func parseSamplingRatio() float64 {
	if s := os.Getenv("EMDEX_OTEL_SAMPLING_RATIO"); s != "" {
		if r, err := strconv.ParseFloat(s, 64); err == nil && r >= 0 && r <= 1 {
			return r
		}
	}
	return 1.0
}
