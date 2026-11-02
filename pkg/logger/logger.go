package logger

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Options configure the logger.
type Options struct {
	// Writer receives one JSON object per line.
	Writer io.Writer

	// Level is the lowest level written.
	Level slog.Level
}

// New returns a JSON logger with slog's default keys that adds the
// trace_id of the span in the context to each line.
func New(opts Options) *slog.Logger {
	handler := slog.NewJSONHandler(opts.Writer, &slog.HandlerOptions{
		Level: opts.Level,
	})

	return slog.New(traceHandler{handler})
}

// traceHandler adds trace_id when the context holds a valid span
// context.
type traceHandler struct {
	slog.Handler
}

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
	}

	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
