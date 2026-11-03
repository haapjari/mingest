package ingest

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

// The stage names, used for the spans and the stage duration metric.
const (
	stageRead  = "read"
	stageParse = "parse"
	stageWrite = "write"
)

// parsedChunk is a chunk with its Mail files identified, ready to commit.
type parsedChunk struct {
	seq   int64
	chunk Chunk
}

// pipeline runs one attempt over the Dataset, in three steps and two
// goroutines joined by one channel:
//
//	Read + Parse ──parsed──▶ Write
//
// Read cuts the archive into chunks and Parse identifies each chunk's Mail
// files, in the same goroutine: parsing is cheap next to gunzip, so it
// gains nothing from one of its own. Write commits the chunks to the Store.
// The channel keeps their order, so Write commits the chunks in archive
// order, which resuming relies on. It also bounds the chunks in memory: at
// most ChannelCapacity + 2, counting the one each goroutine holds.
type pipeline struct {
	o       Options
	store   Store
	tracer  trace.Tracer
	logger  *slog.Logger
	metrics *metrics

	// committed is called with the new Status after each commit.
	committed func(Status)

	// parsed carries the parsed chunks from Read to Write.
	parsed chan parsedChunk
}

// run reads r, skips its first skip Mail files, and commits the rest. It
// returns nil once the whole archive is committed, or else the first error
// of either goroutine. Either way, both have stopped by then.
func (p *pipeline) run(ctx context.Context, r io.Reader, skip int64) error {
	p.parsed = make(chan parsedChunk, p.o.ChannelCapacity)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return p.read(ctx, r, skip) })
	g.Go(func() error { return p.write(ctx) })

	return g.Wait()
}

// buffers updates the gauge of how many chunks wait for Write.
func (p *pipeline) buffers() {
	p.metrics.bufferChunks.Set(float64(len(p.parsed)))
}

// chunkAttributes are the span attributes each stage records for a chunk.
func chunkAttributes(seq int64, mailFiles int) trace.SpanStartEventOption {
	return trace.WithAttributes(
		attribute.Int64("chunk", seq),
		attribute.Int("mail_files", mailFiles),
	)
}

// send sends v on ch unless ctx ends first.
func send[T any](ctx context.Context, ch chan<- T, v T) error {
	select {
	case ch <- v:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// receive takes a value from ch unless ctx ends first. It returns false
// when ch is closed (with a nil error) or when ctx ended (with ctx's error).
func receive[T any](ctx context.Context, ch <-chan T) (T, bool, error) {
	select {
	case v, ok := <-ch:
		return v, ok, nil
	case <-ctx.Done():
		var zero T
		return zero, false, ctx.Err()
	}
}
