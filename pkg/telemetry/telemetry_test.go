package telemetry_test

// The tests set the OTel globals, so none of them runs in parallel.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.uber.org/goleak"

	"mingest/pkg/logger"
	"mingest/pkg/telemetry"
)

func TestNew_GivesSpansTraceIDWithoutEndpoint(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf})
	newTelemetry(t, telemetry.Options{Logger: log})

	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	log.InfoContext(ctx, "inside")
	span.End()

	require.True(t, span.SpanContext().IsValid())
	assert.Equal(
		t,
		span.SpanContext().TraceID().String(),
		lines(t, &buf)[0]["trace_id"],
	)
}

func TestNew_InstallsW3CPropagators(t *testing.T) {
	newTelemetry(t, telemetry.Options{})

	assert.ElementsMatch(t,
		[]string{"traceparent", "tracestate", "baggage"},
		otel.GetTextMapPropagator().Fields(),
	)
}

func TestTelemetry_Shutdown_ExportsToEndpoint(t *testing.T) {
	requests := make(chan string, 1)
	collector := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			requests <- r.Method + " " + r.URL.Path
		},
	))
	defer collector.Close()

	tel := newTelemetry(t, telemetry.Options{Endpoint: collector.URL})

	_, span := otel.Tracer("test").Start(context.Background(), "request")
	span.End()

	require.NoError(t, tel.Shutdown(context.Background()))
	assert.Equal(t, "POST /v1/traces", <-requests)
}

func TestNew_RejectsInvalidEndpoint(t *testing.T) {
	_, err := telemetry.New(context.Background(), telemetry.Options{
		Endpoint: "://collector:4318",
	})

	assert.ErrorContains(t, err, "OTEL_EXPORTER_OTLP_ENDPOINT")
}

// newTelemetry shuts the telemetry down when the test ends, and then
// checks that no goroutine is left behind. Cleanups run last in, first
// out, so the leak check is registered first.
func newTelemetry(
	t *testing.T,
	opts telemetry.Options,
) *telemetry.Telemetry {
	t.Helper()

	t.Cleanup(func() { goleak.VerifyNone(t) })

	if opts.Logger == nil {
		opts.Logger = logger.New(logger.Options{Writer: io.Discard})
	}

	tel, err := telemetry.New(context.Background(), opts)
	require.NoError(t, err)

	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	return tel
}

func lines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var got []map[string]any
	for dec := json.NewDecoder(buf); dec.More(); {
		var line map[string]any
		require.NoError(t, dec.Decode(&line))
		got = append(got, line)
	}

	return got
}
