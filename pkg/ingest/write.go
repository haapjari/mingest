package ingest

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/codes"
)

// write is the Pipeline's Write stage. It commits the chunks as Parse
// passes them on, in archive order, and returns at the first failed commit.
func (p *pipeline) write(ctx context.Context) error {
	for {
		c, ok, err := receive(ctx, p.parsed)
		if !ok {
			return err
		}

		p.buffers()

		if err := p.commitChunk(ctx, c); err != nil {
			return err
		}
	}
}

// commitChunk writes one chunk to the Store in one transaction, then
// updates the metrics and reports the new Status.
func (p *pipeline) commitChunk(ctx context.Context, c parsedChunk) error {
	start := time.Now()

	ctx, span := p.tracer.Start(
		ctx, stageWrite, chunkAttributes(c.seq, len(c.chunk.MailFiles)),
	)
	defer span.End()

	c.chunk.At = start

	st, err := p.store.Commit(ctx, c.chunk)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return err
	}

	p.metrics.stage(stageWrite, start)
	p.metrics.mailFiles.Add(float64(len(c.chunk.MailFiles)))
	p.committed(st)

	return nil
}
