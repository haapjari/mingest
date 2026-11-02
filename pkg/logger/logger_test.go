package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"mingest/pkg/logger"
)

func TestNew_AddsTraceIDInsideSpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf})

	log.InfoContext(inSpan(), "request")

	assert.Equal(t, traceID.String(), decode(t, &buf)["trace_id"])
}

func TestNew_AddsTraceIDAfterWith(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf}).With("step", "read")

	log.InfoContext(inSpan(), "chunk")

	assert.Equal(t, traceID.String(), decode(t, &buf)["trace_id"])
}

func TestNew_AddsTraceIDInsideGroup(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf}).WithGroup("chunk")

	log.InfoContext(inSpan(), "committed")

	group, ok := decode(t, &buf)["chunk"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, traceID.String(), group["trace_id"])
}

func TestNew_OmitsTraceIDOutsideSpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.Options{Writer: &buf})

	log.InfoContext(context.Background(), "starting")

	assert.NotContains(t, decode(t, &buf), "trace_id")
}

var traceID = trace.TraceID{0x0a, 0xf7, 0x65, 0x19, 0x16, 0xcd, 0x43, 0xdd}

func inSpan() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{0x01},
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(context.Background(), sc)
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))

	return line
}
