package ingest

import (
	"context"
	"log/slog"
	"time"

	"mingest/pkg/message"
)

// parseChunk identifies every Mail file in the chunk.
func (p *pipeline) parseChunk(ctx context.Context, c rawChunk) parsedChunk {
	start := time.Now()

	ctx, span := p.tracer.Start(
		ctx, stageParse, chunkAttributes(c.seq, len(c.files)),
	)
	defer span.End()

	out := parsedChunk{
		seq: c.seq,
		chunk: Chunk{
			MailFiles: make([]message.Identity, 0, len(c.files)),
			BytesRead: c.bytesRead,
		},
	}

	for _, f := range c.files {
		id := message.Identify(f.scanned)
		out.chunk.MailFiles = append(out.chunk.MailFiles, id)
		p.check(ctx, f, id)
	}

	p.metrics.stage(stageParse, start)

	return out
}

// check logs a warning when a Mail file has no Sender or a header field
// was cut at the size cap.
func (p *pipeline) check(ctx context.Context, f rawFile, id message.Identity) {
	if id.Sender == "" {
		p.logger.WarnContext(ctx, "mail file has no sender",
			slog.String("path", f.path))
	}

	for field := range f.scanned.Truncated.All() {
		p.logger.WarnContext(ctx, "header field truncated",
			slog.String("path", f.path), slog.String("field", field.String()))
	}
}
