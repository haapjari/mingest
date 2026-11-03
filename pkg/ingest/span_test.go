package ingest

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"mingest/test/dataset"
)

func TestPipeline_Run_RecordsSpanPerChunkPerStage(t *testing.T) {
	t.Parallel()

	spans := tracetest.NewSpanRecorder()
	p := newTestPipeline(newFakeStore(), pipelineOptions(5))
	p.tracer = sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(spans),
	).Tracer("")

	require.NoError(t, p.run(t.Context(), bytes.NewReader(fixture()), 0))

	for _, stage := range []string{stageRead, stageParse, stageWrite} {
		chunks, mailFiles := spanTotals(spans.Ended(), stage)
		assert.Equal(t, []int64{0, 1, 2, 3, 4}, chunks, stage)
		assert.Equal(t, int64(dataset.FixtureMailFiles), mailFiles, stage)
	}
}

// spanTotals returns the chunk numbers of the spans named stage, in order,
// and the sum of their mail_files.
func spanTotals(spans []sdktrace.ReadOnlySpan, stage string) ([]int64, int64) {
	var (
		chunks    []int64
		mailFiles int64
	)

	for _, s := range named(spans, stage) {
		chunks = append(chunks, attr(s, "chunk"))
		mailFiles += attr(s, "mail_files")
	}

	slices.Sort(chunks)

	return chunks, mailFiles
}

func named(spans []sdktrace.ReadOnlySpan, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == name {
			out = append(out, s)
		}
	}

	return out
}

func attr(s sdktrace.ReadOnlySpan, key attribute.Key) int64 {
	for _, kv := range s.Attributes() {
		if kv.Key == key {
			return kv.Value.AsInt64()
		}
	}

	return -1
}
