package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

func TestRun_ServesUntilTheContextEnds(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)

	go func() { errs <- run(ctx, appEnviron(t), out) }()

	require.Eventually(t, func() bool {
		return strings.Contains(out.String(), `"msg":"listening"`)
	}, 10*time.Second, 10*time.Millisecond)

	cancel()

	require.NoError(t, <-errs)
	assert.Contains(t, out.String(), `"msg":"shutting down"`)
	assert.Contains(t, out.String(), `"level":"INFO","msg":"stopped"`)
	assert.NotContains(t, out.String(), "secret", "the source is redacted")
}

func TestRedacted_HidesThePassword(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://user:xxxxx@host/x.tar.gz",
		redacted("https://user:secret@host/x.tar.gz"))
	assert.Equal(t, "(unparsable)", redacted("://"))
}

// appEnviron runs mingest on the fixture, with its own data directory and
// free ports. A password in the source checks it's redacted.
func appEnviron(t *testing.T) []string {
	t.Helper()

	source := strings.Replace(dataset.WriteFile(t, dataset.Fixture()),
		"file://", "file://user:secret@", 1)

	return []string{
		"MINGEST_DATASET_SOURCE=" + source,
		"MINGEST_DATA_DIR=" + t.TempDir(),
		"MINGEST_LISTEN_ADDRESS=127.0.0.1:0",
		"MINGEST_ADMIN_LISTEN_ADDRESS=127.0.0.1:0",
	}
}

// syncBuffer is a bytes.Buffer safe to write from several goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
