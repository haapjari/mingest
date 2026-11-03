package server_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/ingest"
)

func TestServer_Handler_StreamStartsWhileIdleThenPings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ts := newTestServer(t)
		s := openStream(t, ts.handler, "")

		assert.Equal(t, "retry: 3000\n\n"+
			"event: status\n"+
			"data: "+idleData+"\n\n", s.read())

		time.Sleep(15 * time.Second)
		assert.Equal(t, ": ping\n\n", s.read())

		ts.ing.set(revision(runningStatus(), 1))
		assert.Equal(t, "event: status\n"+
			"id: 019a3b1c-0000-7000-8000-000000000001:1\n"+
			"data: "+runningData+"\n\n", s.read())
	})
}

func TestServer_Handler_StreamSendsOnlyTheLatestToSlowClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ts := newTestServer(t)
		ts.ing.status = revision(runningStatus(), 1)
		s := openStream(t, ts.handler, "")
		s.read()

		s.rec.hold()
		ts.ing.set(revision(runningStatus(), 2))
		synctest.Wait()
		ts.ing.set(revision(runningStatus(), 3))
		ts.ing.set(revision(runningStatus(), 4))
		s.rec.release()

		assert.Equal(t, []string{"2", "4"}, revisions(s.read()),
			"revision 3 came and went while the client was busy")
	})
}

func TestServer_Handler_StreamWaitsPastTheClientsLastEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ts := newTestServer(t)
		ts.ing.status = revision(runningStatus(), 2)
		s := openStream(t, ts.handler, runningStatus().ID+":2")

		assert.Equal(t, "retry: 3000\n\n", s.read())

		ts.ing.set(revision(runningStatus(), 3))
		assert.Equal(t, []string{"3"}, revisions(s.read()))
	})
}

func TestServer_Handler_StreamClosesOnStatusError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ts := newTestServer(t)
		ts.ing.status = revision(runningStatus(), 1)
		s := openStream(t, ts.handler, "")
		s.read()

		ts.ing.fail(errors.New("snapshot gone"))

		require.True(t, s.closed())

		failed := ts.logs.find(t, "stream status failed")
		require.Len(t, failed, 1)
		assert.Equal(t, "ERROR", failed[0]["level"])
		assert.Equal(t, "snapshot gone", failed[0]["error"])
		assert.Equal(t, "error", ts.closedLine(t)["reason"])
	})
}

func TestServer_Handler_StreamClosesWhenTheClientStopsReading(t *testing.T) {
	tests := map[string]func(ts *testServer){
		"at the first write": func(*testServer) {},
		"at an event": func(ts *testServer) {
			ts.ing.set(revision(runningStatus(), 2))
		},
		"at a ping": func(*testServer) { time.Sleep(15 * time.Second) },
	}

	for name, after := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ts := newTestServer(t)
				ts.ing.status = revision(runningStatus(), 1)
				rec := &streamRecorder{header: http.Header{}}

				if name == "at the first write" {
					rec.breakConn()
				}

				s := openStreamTo(t, ts.handler, "", rec)
				s.read()
				rec.breakConn()
				after(ts)

				require.True(t, s.closed())
				assert.Equal(t, "client", ts.closedLine(t)["reason"])
			})
		})
	}
}

func TestServer_Handler_StreamAnswersErrorWhenStatusFails(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.statusErr = errors.New("snapshot gone")

	resp := getStatus(t, ts.handler, "text/event-stream")

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Equal(t, "The server failed to read the ingestion status.",
		decodeProblem(t, resp)["detail"])
}

func TestServer_Handler_StreamOpenedAtTheEndSendsOneEvent(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.status = revision(doneStatus(), 7)

	resp := getStatus(t, ts.handler, "text/event-stream")

	assert.Equal(t, []string{"7"}, revisions(resp.Body.String()))
	assert.Len(t, ts.logs.find(t, "stream opened"), 1)
	assert.Equal(t, "terminal", ts.closedLine(t)["reason"])
	assert.InDelta(t, 1, ts.closedLine(t)["events"], 0)
}

func FuzzLastEventID(f *testing.F) {
	for _, seed := range []string{
		"", doneStatus().ID + ":7", doneStatus().ID + ":6", ":", "x:y",
		strings.Repeat("9", 30), "\x00",
	} {
		f.Add(seed)
	}

	ts := newTestServer(f)
	ts.ing.status = revision(doneStatus(), 7)

	f.Fuzz(func(t *testing.T, id string) {
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/status", nil,
		)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Last-Event-ID", id)

		resp := httptest.NewRecorder()
		ts.handler.ServeHTTP(resp, req)

		switch resp.Code {
		case http.StatusOK:
			assert.Contains(t, resp.Body.String(), "event: status\n")
		case http.StatusNoContent:
		default:
			t.Fatalf("Last-Event-ID %q gave %d, want 200 or 204", id, resp.Code)
		}
	})
}

// openedStream is a GET /status stream served in the background.
type openedStream struct {
	rec    *streamRecorder
	hangUp context.CancelFunc
	done   chan struct{}
}

// openStream asks h for the stream, with lastEventID unless it's empty.
func openStream(
	t *testing.T,
	h http.Handler,
	lastEventID string,
) *openedStream {
	t.Helper()

	rec := &streamRecorder{header: http.Header{}}

	return openStreamTo(t, h, lastEventID, rec)
}

// openStreamTo is openStream writing to the given recorder.
func openStreamTo(
	t *testing.T,
	h http.Handler,
	lastEventID string,
	rec *streamRecorder,
) *openedStream {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/status", nil)
	req.Header.Set("Accept", "text/event-stream")

	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	s := &openedStream{
		rec:    rec,
		hangUp: cancel,
		done:   make(chan struct{}),
	}

	go func() {
		defer close(s.done)
		h.ServeHTTP(s.rec, req)
	}()

	t.Cleanup(func() {
		cancel()
		<-s.done
	})

	return s
}

// read waits until the stream's handler blocks, and returns what it wrote
// since the last read.
func (s *openedStream) read() string {
	synctest.Wait()

	return s.rec.take()
}

// closed waits until the handler blocks or returns, and reports whether it
// returned.
func (s *openedStream) closed() bool {
	synctest.Wait()

	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// streamRecorder records a streamed response. While held, its writes block,
// like a client that doesn't read. Once broken, they fail, like a client
// that left.
type streamRecorder struct {
	mu     sync.Mutex
	header http.Header
	code   int
	sentH  http.Header
	body   bytes.Buffer
	gate   chan struct{}
	broken bool
}

// errClientGone is a write to a broken streamRecorder.
var errClientGone = errors.New("client gone")

func (r *streamRecorder) breakConn() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.broken = true
}

func (r *streamRecorder) Header() http.Header { return r.header }

func (r *streamRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.code == 0 {
		r.code, r.sentH = code, r.header.Clone()
	}
}

func (r *streamRecorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)

	r.mu.Lock()
	gate := r.gate
	r.mu.Unlock()

	if gate != nil {
		<-gate
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.broken {
		return 0, errClientGone
	}

	return r.body.Write(p)
}

func (r *streamRecorder) Flush() {}

func (r *streamRecorder) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := r.body.String()
	r.body.Reset()

	return out
}

func (r *streamRecorder) hold() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.gate = make(chan struct{})
}

func (r *streamRecorder) release() {
	r.mu.Lock()
	defer r.mu.Unlock()

	close(r.gate)
	r.gate = nil
}

// revisions returns the revision of each event id in out, in order.
func revisions(out string) []string {
	var revs []string
	for line := range strings.SplitSeq(out, "\n") {
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			_, rev, _ := strings.Cut(id, ":")
			revs = append(revs, rev)
		}
	}

	return revs
}

func revision(st ingest.Status, rev int64) ingest.Status {
	st.Revision = rev

	return st
}

// closedLine is the one stream closed line.
func (ts *testServer) closedLine(t *testing.T) map[string]any {
	t.Helper()

	lines := ts.logs.find(t, "stream closed")
	require.Len(t, lines, 1)

	return lines[0]
}

// idleData and runningData are the data lines of an idle and a running
// status event.
const (
	idleData = `{"ingestion_id":null,"state":"idle",` +
		`"mail_files_processed":0,"messages_processed":0,"senders":0,` +
		`"messages_without_sender":0,"bytes_read":0,"bytes_total":null,` +
		`"progress_percent":null,"resumes":0,"started_at":null,` +
		`"updated_at":null,"finished_at":null,"error":null}`

	runningData = `{"ingestion_id":"019a3b1c-0000-7000-8000-000000000001",` +
		`"state":"running","mail_files_processed":5,` +
		`"messages_processed":4,"senders":3,"messages_without_sender":1,` +
		`"bytes_read":999,"bytes_total":1000,"progress_percent":99.9,` +
		`"resumes":1,"started_at":"2026-11-01T09:30:12Z",` +
		`"updated_at":"2026-11-01T09:31:12Z","finished_at":null,` +
		`"error":null}`
)
