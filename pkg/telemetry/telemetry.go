package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
)

// tracesPath is appended to OTEL_EXPORTER_OTLP_ENDPOINT, as the OTel
// specification says for the endpoint shared by all signals.
const tracesPath = "v1/traces"

// Options configure the telemetry.
type Options struct {
	// Endpoint is OTEL_EXPORTER_OTLP_ENDPOINT. Spans are exported only
	// when it is set.
	Endpoint string

	// Logger logs the OTel SDK's errors, such as a failed export.
	Logger *slog.Logger
}

// Telemetry owns the tracer provider and the Prometheus registry.
type Telemetry struct {
	provider *trace.TracerProvider
	registry *prometheus.Registry
}

// New installs the tracer provider, the propagators and the error
// handler as the OTel globals, and builds the registry.
func New(ctx context.Context, opts Options) (*Telemetry, error) {
	provider, err := tracerProvider(ctx, opts.Endpoint)
	if err != nil {
		return nil, err
	}

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		opts.Logger.ErrorContext(
			context.Background(), "telemetry failed", slog.Any("error", err),
		)
	}))

	return &Telemetry{provider: provider, registry: newRegistry()}, nil
}

// Registry is where the other packages register their instruments.
func (t *Telemetry) Registry() *prometheus.Registry {
	return t.registry
}

// Shutdown exports the remaining spans and stops the tracer provider.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return t.provider.Shutdown(ctx)
}

func tracerProvider(
	ctx context.Context,
	endpoint string,
) (*trace.TracerProvider, error) {
	opts := []trace.TracerProviderOption{
		trace.WithSampler(trace.ParentBased(trace.AlwaysSample())),
	}

	if endpoint != "" {
		traces, err := url.JoinPath(endpoint, tracesPath)
		if err != nil {
			return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %w", err)
		}

		exporter, err := otlptracehttp.New(
			ctx, otlptracehttp.WithEndpointURL(traces),
		)
		if err != nil {
			return nil, fmt.Errorf("creating the OTLP exporter: %w", err)
		}

		opts = append(opts, trace.WithBatcher(exporter))
	}

	return trace.NewTracerProvider(opts...), nil
}

func newRegistry() *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return registry
}
