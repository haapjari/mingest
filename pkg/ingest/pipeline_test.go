package ingest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/goleak"

	"mingest/pkg/message"
)

func TestPipeline_Run_CommitsInArchiveOrder(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	p := newTestPipeline(store, pipelineOptions(1))

	require.NoError(t, p.run(t.Context(), bytes.NewReader(fixture()), 0))

	assert.Equal(t, identities(archiveFiles(t, fixture())),
		committedIdentities(store))
}

func TestPipeline_Run_BoundsChunksInMemory(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		o := pipelineOptions(1)

		var commits atomic.Int64

		store := newFakeStore()
		store.onCommit = func(ctx context.Context, _ Chunk) error {
			commits.Add(1)
			<-ctx.Done()

			return ctx.Err()
		}

		ctx, cancel := context.WithCancel(t.Context())
		p := newTestPipeline(store, o)
		errs := make(chan error, 1)

		go func() { errs <- p.run(ctx, bytes.NewReader(fixture()), 0) }()

		// Write holds one chunk in Commit and the buffer is full, so Read,
		// which handles one chunk at a time, waits with the next.
		synctest.Wait()
		assert.Equal(t, int64(1), commits.Load())
		assert.Len(t, p.parsed, o.ChannelCapacity)

		cancel()
		require.ErrorIs(t, <-errs, context.Canceled)
	})
}

func TestPipeline_Run_StopsAtCommitError(t *testing.T) {
	t.Parallel()

	errCommit := errors.New("commit failed")
	store := newFakeStore()
	store.onCommit = func(_ context.Context, c Chunk) error {
		if len(store.committed()) == 1 {
			return errCommit
		}

		return nil
	}

	p := newTestPipeline(store, pipelineOptions(1))

	err := p.run(t.Context(), bytes.NewReader(fixture()), 0)
	require.ErrorIs(t, err, errCommit)

	assert.Len(t, store.committed(), 1)
}

func TestPipeline_Run_ReturnsReadErrors(t *testing.T) {
	t.Parallel()

	data := fixture()
	p := newTestPipeline(newFakeStore(), pipelineOptions(1))

	err := p.run(t.Context(), bytes.NewReader(data[:len(data)/2]), 0)
	require.ErrorIs(t, err, errArchiveCorrupt)
}

// Not parallel: goleak sees every goroutine in the process.
func TestPipeline_Run_DiscardsUncommittedChunkWhenCancelled(t *testing.T) {
	running := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, running) })

	ctx, cancel := context.WithCancel(t.Context())
	store := newFakeStore()
	store.onCommit = func(context.Context, Chunk) error {
		cancel()
		return ctx.Err()
	}

	p := newTestPipeline(store, pipelineOptions(1))

	err := p.run(ctx, bytes.NewReader(fixture()), 0)
	require.ErrorIs(t, err, context.Canceled)

	assert.Empty(t, store.committed())
}

// pipelineOptions cuts a chunk every chunkMailFiles Mail files.
func pipelineOptions(chunkMailFiles int) Options {
	o := options(chunkMailFiles)
	o.ChannelCapacity = 2

	return o
}

func newTestPipeline(store Store, o Options) *pipeline {
	return &pipeline{
		o:         o,
		store:     store,
		tracer:    noop.NewTracerProvider().Tracer(""),
		logger:    slog.New(slog.DiscardHandler),
		metrics:   newMetrics(),
		committed: func(Status) {},
	}
}

// archiveFiles returns an archive's Mail files in archive order.
func archiveFiles(t *testing.T, data []byte) []rawFile {
	t.Helper()

	chunks, err := collect(data, 0, options(1))
	require.NoError(t, err)

	var out []rawFile
	for _, c := range chunks {
		out = append(out, c.files...)
	}

	return out
}

// value reads a counter or gauge.
func value(t *testing.T, m prometheus.Metric) float64 {
	t.Helper()

	var out dto.Metric
	require.NoError(t, m.Write(&out))

	return out.GetCounter().GetValue() + out.GetGauge().GetValue()
}

func identities(files []rawFile) []message.Identity {
	out := make([]message.Identity, 0, len(files))
	for _, f := range files {
		out = append(out, message.Identify(f.scanned))
	}

	return out
}

func committedIdentities(s *fakeStore) []message.Identity {
	var out []message.Identity
	for _, c := range s.committed() {
		out = append(out, c.MailFiles...)
	}

	return out
}
