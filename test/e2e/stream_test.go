//go:build e2e

package e2e_test

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

func TestStatus_StreamsUntilDone(t *testing.T) {
	t.Parallel()

	p := start(t, serveSlowly(t, dataset.Fixture(), fixtureStep).env...)
	do(t, http.MethodPost, p.URL+"/start")

	stream := getWith(t, p.URL+"/status", "Accept", "text/event-stream")
	require.Equal(t, http.StatusOK, stream.status)

	events, err := contract.ReadEvents(strings.NewReader(stream.body))
	require.NoError(t, err)
	require.NotEmpty(t, events)

	last := events[len(events)-1]
	again := getWith(t, p.URL+"/status",
		"Accept", "text/event-stream", "Last-Event-ID", last.ID)
	assert.Equal(t, http.StatusNoContent, again.status)
}

func TestStatus_RefusesStreamsPastTheCap(t *testing.T) {
	t.Parallel()

	p := start(t, "MINGEST_SSE_MAX_STREAMS=1")

	first := openStream(t, p.URL)
	assert.Equal(t, http.StatusOK, first.status)

	second := getWith(t, p.URL+"/status", "Accept", "text/event-stream")
	assert.Equal(t, http.StatusServiceUnavailable, second.status)
	assert.Equal(t, "5", second.header.Get("Retry-After"))
	assert.Equal(t, "tag:mingest,2026:too-many-streams",
		second.problem(t)["type"])

	assert.Equal(t, http.StatusOK, get(t, p.URL+"/status").status,
		"JSON takes no stream slot")
}

// This test takes 16 s: the ping comes at 15 s, past both 10 s timeouts.
func TestStatus_StreamFlushesAndOutlivesTheTimeouts(t *testing.T) {
	t.Parallel()

	p := start(t)
	lines := openStream(t, p.URL).lines
	opened := time.Now()

	for _, want := range []string{"retry: 3000", "", "event: status"} {
		assert.Equal(t, want, nextLine(t, lines, 2*time.Second))
	}

	assert.True(t, strings.HasPrefix(nextLine(t, lines, time.Second),
		`data: {"ingestion_id":null,"state":"idle"`))
	assert.Empty(t, nextLine(t, lines, time.Second))

	assert.Equal(t, ": ping", nextLine(t, lines, 20*time.Second))
	assert.Greater(t, time.Since(opened), 14*time.Second)
	assert.Empty(t, nextLine(t, lines, time.Second))

	select {
	case line, ok := <-lines:
		t.Fatalf("after the ping, got %q (open %t), want nothing", line, ok)
	case <-time.After(time.Until(opened.Add(16 * time.Second))):
	}
}

func TestStop_EndsOpenStreams(t *testing.T) {
	t.Parallel()

	p := start(t)
	lines := openStream(t, p.URL).lines
	assert.Equal(t, "retry: 3000", nextLine(t, lines, 2*time.Second))

	stopping := time.Now()
	p.Stop(t)
	assert.Less(t, time.Since(stopping), 5*time.Second)

	for range lines {
	}
}

// liveStream is an open GET /status stream, read line by line in the
// background. lines closes when the stream ends.
type liveStream struct {
	status int
	lines  <-chan string
}

// openStream opens GET /status as a stream through the contract's client.
// It's closed when the test ends.
func openStream(t *testing.T, url string) liveStream {
	t.Helper()

	req := request(t, http.MethodGet, url+"/status")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := contract.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	lines := make(chan string)

	go func() {
		defer close(lines)

		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-t.Context().Done():
				return
			}
		}
	}()

	return liveStream{status: resp.StatusCode, lines: lines}
}

// nextLine waits for the next line, for at most within.
func nextLine(t *testing.T, lines <-chan string, within time.Duration) string {
	t.Helper()

	select {
	case line, ok := <-lines:
		require.True(t, ok, "the stream ended")

		return line
	case <-time.After(within):
		t.Fatalf("no line within %s", within)

		return ""
	}
}
