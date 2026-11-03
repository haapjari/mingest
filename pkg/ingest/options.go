package ingest

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Options configures the Ingester. run fills the numbers from
// config.Pipeline.
type Options struct {
	// ChunkMailFiles closes a chunk after this many Mail files.
	ChunkMailFiles int

	// ChunkBytes closes a chunk before the Mail file that would take it
	// past this many bytes.
	ChunkBytes int64

	// ChunkAge closes a chunk this long after its first Mail file.
	ChunkAge time.Duration

	// ReadBuffer is the buffer between the Source and gunzip.
	ReadBuffer int

	// ChannelCapacity is how many chunks the channel from Read to Write
	// holds. The Pipeline holds at most ChannelCapacity + 2 chunks.
	ChannelCapacity int

	// SourceRetries is how many times in a row a failed Source is retried.
	SourceRetries int

	// RetryBase and RetryMax bound the full-jitter backoff between retries.
	RetryBase, RetryMax time.Duration

	// SourceURL is the Dataset's URL, which Begin records and a resume
	// checks the Stream against.
	SourceURL string

	// Logger gets the lifecycle and per-Mail-file log lines.
	Logger *slog.Logger

	// Registerer gets the Ingester's metrics. Nil leaves them
	// unregistered.
	Registerer prometheus.Registerer
}
