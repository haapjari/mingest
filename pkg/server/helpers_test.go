package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"mingest/pkg/ingest"
	"mingest/pkg/logger"
)

// fakeIngester answers with settable values.
type fakeIngester struct {
	mu sync.Mutex

	// Start returns started and startErr, and keeps the context it got.
	started  ingest.Status
	startErr error
	startCtx context.Context

	// TopSenders returns top and topErr.
	top    ingest.TopSenders
	topErr error

	// Status returns status and statusErr, or panics with statusPanic.
	status      ingest.Status
	statusErr   error
	statusPanic any

	changed chan struct{}
}

func (f *fakeIngester) Start(ctx context.Context) (ingest.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.startCtx = ctx

	return f.started, f.startErr
}

func (f *fakeIngester) Status(context.Context) (ingest.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.statusPanic != nil {
		panic(f.statusPanic)
	}

	return f.status, f.statusErr
}

func (f *fakeIngester) Changed() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.changed == nil {
		f.changed = make(chan struct{})
	}

	return f.changed
}

func (f *fakeIngester) TopSenders(context.Context) (ingest.TopSenders, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.top, f.topErr
}

// set makes the given Status current and closes the Changed channel, as a
// write does.
func (f *fakeIngester) set(st ingest.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.status = st
	f.notify()
}

// fail makes Status fail with err from the next read on, and closes the
// Changed channel.
func (f *fakeIngester) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.statusErr = err
	f.notify()
}

func (f *fakeIngester) notify() {
	if f.changed != nil {
		close(f.changed)
	}

	f.changed = make(chan struct{})
}

// do sends a request to h and returns the recorded response.
func do(
	t *testing.T,
	h http.Handler,
	method, path string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	return resp
}

// decodeProblem decodes a problem body.
func decodeProblem(
	t *testing.T,
	resp *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()

	var p map[string]any
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &p))

	return p
}

// logBuffer collects JSON log lines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// logger is the service's logger, trace_id included, writing to b.
func (b *logBuffer) logger(level slog.Level) *slog.Logger {
	return logger.New(logger.Options{Writer: b, Level: level})
}

// find returns the lines whose msg is msg.
func (b *logBuffer) find(t *testing.T, msg string) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var out []map[string]any

	lines := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for lines.Scan() {
		var line map[string]any
		require.NoError(t, json.Unmarshal(lines.Bytes(), &line))

		if line["msg"] == msg {
			out = append(out, line)
		}
	}

	return out
}
