package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/ingest"
)

func TestServer_Handler_StartAcceptsFromEveryEndState(t *testing.T) {
	t.Parallel()

	for _, state := range []ingest.State{
		ingest.StateIdle, ingest.StateDone, ingest.StateFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			ts := newTestServer(t)
			ts.ing.status = ingest.Status{State: state}
			ts.ing.started = runningStatus()

			resp := do(t, ts.handler, http.MethodPost, "/start")

			require.Equal(t, http.StatusAccepted, resp.Code)
			assert.Equal(t, "/status", resp.Header().Get("Location"))
			assert.Equal(t, "application/json",
				resp.Header().Get("Content-Type"))
			assert.JSONEq(t, runningJSON, resp.Body.String())
		})
	}
}

func TestServer_Handler_StartHidesOtherErrors(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.startErr = errors.New("disk on fire")

	resp := do(t, ts.handler, http.MethodPost, "/start")

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Equal(t, "The server failed to start the ingestion.",
		decodeProblem(t, resp)["detail"])
	assert.NotContains(t, resp.Body.String(), "disk on fire")

	lines := ts.logs.find(t, "request")
	require.Len(t, lines, 1)
	assert.Equal(t, "ERROR", lines[0]["level"])
	assert.Equal(t, "disk on fire", lines[0]["error"])
}

func TestServer_Handler_StartIgnoresClientHangUp(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/start", nil)
	ts.handler.ServeHTTP(httptest.NewRecorder(), req)

	require.NotNil(t, ts.ing.startCtx)
	assert.NoError(t, ts.ing.startCtx.Err(), "Start's context isn't cancelled")
}

func TestServer_Handler_StatusNegotiatesAccept(t *testing.T) {
	t.Parallel()

	// want is the media type answered, or "" for 406. The fake is done,
	// so every stream ends after one event.
	const json, stream, refused = "application/json", "text/event-stream", ""

	tests := map[string]struct {
		accept string
		want   string
	}{
		"no Accept":            {"", json},
		"application/json":     {"application/json", json},
		"*/*":                  {"*/*", json},
		"application/*":        {"application/*", json},
		"a browser navigation": {"text/html,*/*;q=0.8", json},
		"text/html":            {"text/html", refused},
		"EventSource":          {"text/event-stream", stream},
		"text/*":               {"text/*", stream},
		"JSON wins over the stream": {
			accept: "text/event-stream, application/json", want: json,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ts := newTestServer(t)
			ts.ing.status = doneStatus()
			resp := getStatus(t, ts.handler, tc.accept)

			want := http.StatusNotAcceptable
			if tc.want != refused {
				want = http.StatusOK
			}

			assert.Equal(t, want, resp.Code)
			assert.Equal(t, tc.want, mediaType(resp))
			assert.Equal(t, "Accept", resp.Header().Get("Vary"))
		})
	}
}

func TestServer_Handler_StatusHidesItsError(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.statusErr = errors.New("snapshot gone")

	resp := do(t, ts.handler, http.MethodGet, "/status")

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Equal(t, "The server failed to read the ingestion status.",
		decodeProblem(t, resp)["detail"])
	assert.NotContains(t, resp.Body.String(), "snapshot gone")
}

func TestServer_Handler_RecoversFromPanicWithProblem(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.statusPanic = "boom"

	resp := do(t, ts.handler, http.MethodGet, "/status")

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Regexp(t, `^[0-9a-f]{32}$`, decodeProblem(t, resp)["trace_id"])

	lines := ts.logs.find(t, "request")
	require.Len(t, lines, 1)
	assert.Equal(t, "ERROR", lines[0]["level"])
	assert.Contains(t, lines[0]["error"], "panic: boom")
	assert.Contains(t, lines[0]["error"], "goroutine", "the stack")
}

func TestServer_Handler_RecoversFromErrorPanicWithProblem(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.statusPanic = errors.New("boom")

	resp := do(t, ts.handler, http.MethodGet, "/status")

	assert.Equal(t, http.StatusInternalServerError, resp.Code)
}

func TestServer_Handler_AbortsOnErrAbortHandler(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.statusPanic = http.ErrAbortHandler

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		do(t, ts.handler, http.MethodGet, "/status")
	})
}

func TestServer_Handler_TopSendersHidesItsError(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	ts.ing.topErr = errors.New("snapshot gone")

	resp := do(t, ts.handler, http.MethodGet, "/top-senders")

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Equal(t, "The server failed to read the top senders.",
		decodeProblem(t, resp)["detail"])
	assert.NotContains(t, resp.Body.String(), "snapshot gone")
}

func FuzzAccept(f *testing.F) {
	for _, seed := range []string{
		"", "application/json", "text/event-stream", "*/*;q=0",
		"text/html,*/*;q=0.8", "application/json;q=0.5, text/event-stream",
		";;,,q=", "*/*;q=NaN", "application/json;q=1e400",
	} {
		f.Add(seed)
	}

	// The fake is done, so every stream ends after one event.
	ts := newTestServer(f)
	ts.ing.status = doneStatus()

	f.Fuzz(func(t *testing.T, accept string) {
		resp := getStatus(t, ts.handler, accept)

		switch resp.Code {
		case http.StatusOK:
			assert.Contains(t,
				[]string{"application/json", "text/event-stream"},
				mediaType(resp))
		case http.StatusNotAcceptable:
		default:
			t.Fatalf("Accept %q gave %d, want 200 or 406", accept, resp.Code)
		}
	})
}

// getStatus sends GET /status with accept as its Accept header, or none
// when it's empty.
func getStatus(
	t *testing.T,
	h http.Handler,
	accept string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/status", nil,
	)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)

	return resp
}

// mediaType is the response's media type, or "" for a problem.
func mediaType(resp *httptest.ResponseRecorder) string {
	if resp.Code != http.StatusOK {
		return ""
	}

	return resp.Header().Get("Content-Type")
}

// at is a time in a zone east of UTC, which the body writes in UTC.
var at = time.Date(2026, time.November, 1, 11, 30, 12, 0,
	time.FixedZone("EET", 2*60*60))

func runningStatus() ingest.Status {
	return ingest.Status{
		ID:                    "019a3b1c-0000-7000-8000-000000000001",
		State:                 ingest.StateRunning,
		MailFiles:             5,
		Messages:              4,
		Senders:               3,
		MessagesWithoutSender: 1,
		BytesRead:             999,
		BytesTotal:            1000,
		Resumes:               1,
		StartedAt:             at,
		UpdatedAt:             at.Add(time.Minute),
		SourceURL:             "file:///never/served",
	}
}

func doneStatus() ingest.Status {
	st := runningStatus()
	st.State = ingest.StateDone
	st.BytesRead = 1000
	st.FinishedAt = at.Add(2 * time.Minute)

	return st
}

const runningJSON = `{
	"ingestion_id": "019a3b1c-0000-7000-8000-000000000001",
	"state": "running",
	"mail_files_processed": 5,
	"messages_processed": 4,
	"senders": 3,
	"messages_without_sender": 1,
	"bytes_read": 999,
	"bytes_total": 1000,
	"progress_percent": 99.9,
	"resumes": 1,
	"started_at": "2026-11-01T09:30:12Z",
	"updated_at": "2026-11-01T09:31:12Z",
	"finished_at": null,
	"error": null
}`
