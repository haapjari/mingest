//go:build e2e

package e2e_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
	"mingest/test/harness"
)

func TestStop_ExitsCleanlyOnSIGTERM(t *testing.T) {
	t.Parallel()

	p := start(t)
	p.Stop(t)

	assert.NotEmpty(t, logLines(t, p.Log(), "shutting down"))
	assert.NotEmpty(t, logLines(t, p.Log(), "stopped"))
}

func TestRun_RefusesLockedDataDirectory(t *testing.T) {
	t.Parallel()

	dir := "MINGEST_DATA_DIR=" + t.TempDir()
	first := start(t, dir)

	second := harness.Run(t, bin, nil, source(t), dir)
	assert.Equal(t, 1, second.Code)
	assert.Contains(t, second.Output, "data directory in use")
	assert.Equal(t, http.StatusOK, get(t, first.URL+"/status").status)

	first.Stop(t)
	start(t, dir)
}

func TestRun_FailsStartupNamingTheCause(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	taken, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	tests := map[string]struct {
		env   []string
		cause string
	}{
		"unknown scheme": {
			env: []string{"MINGEST_DATASET_SOURCE=ftp://x"}, cause: "ftp",
		},
		"missing data directory": {
			env:   []string{source(t), "MINGEST_DATA_DIR="},
			cause: "MINGEST_DATA_DIR",
		},
		"unknown log level": {
			env:   []string{source(t), "MINGEST_LOG_LEVEL=verbose"},
			cause: "verbose",
		},
		"listen address in use": {
			env: []string{
				source(t), "MINGEST_LISTEN_ADDRESS=" + taken.Addr().String(),
			},
			cause: "address already in use",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := harness.Run(t, bin, nil, tc.env...)

			assert.Equal(t, 1, r.Code)
			assert.Contains(t, r.Output, `"msg":"startup failed"`)
			assert.Contains(t, r.Output, tc.cause)
		})
	}
}

func TestRun_RejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	r := harness.Run(t, bin, []string{"foo"})

	assert.Equal(t, 2, r.Code)
	assert.Contains(t, r.Output, "usage: mingest [healthcheck]")
}

func TestHandler_AnswersUnknownPathWithNotFound(t *testing.T) {
	t.Parallel()

	r := get(t, start(t).URL+"/nope")

	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Regexp(t, traceID, r.problem(t)["trace_id"])
}

func TestAdmin_ServesMetricsAndProfiles(t *testing.T) {
	t.Parallel()

	p := start(t)

	metrics := admin(t, p.AdminURL+"/metrics").body
	for _, name := range []string{
		"mingest_mail_files_total",
		"mingest_sse_streams_max 100",
		"go_gc_gomemlimit_bytes",
	} {
		assert.Contains(t, metrics, name)
	}

	assert.Equal(t, http.StatusOK, admin(t, p.AdminURL+"/debug/pprof/").status)
}

func TestHandler_LogsOneLinePerRequest(t *testing.T) {
	t.Parallel()

	p := start(t)
	get(t, p.URL+"/top-senders")
	get(t, p.URL+"/nope")

	found := requestLine(t, p, "/top-senders")
	missing := requestLine(t, p, "/nope")

	assert.Equal(t, "INFO", found["level"])
	assert.InDelta(t, http.StatusOK, found["status"], 0)
	assert.Regexp(t, traceID, found["trace_id"])
	assert.InDelta(t, http.StatusNotFound, missing["status"], 0)
	assert.Regexp(t, traceID, missing["trace_id"])
}

// traceID is a trace id as the server writes it.
const traceID = `^[0-9a-f]{32}$`

// start starts mingest on the fixture, then env.
func start(t *testing.T, env ...string) *harness.Process {
	t.Helper()

	return harness.Start(t, bin, append([]string{source(t)}, env...)...)
}

// source is MINGEST_DATASET_SOURCE for the fixture, written to a file.
func source(t *testing.T) string {
	t.Helper()

	return "MINGEST_DATASET_SOURCE=" + dataset.WriteFile(t, dataset.Fixture())
}

// reply is a response, read in full.
type reply struct {
	status int
	header http.Header
	body   string
}

// problem decodes the reply's problem body.
func (r reply) problem(t *testing.T) map[string]any {
	t.Helper()

	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.body), &p))

	return p
}

// get sends a GET through the contract's client and fails on an error.
func get(t *testing.T, url string) reply {
	t.Helper()

	return do(t, http.MethodGet, url)
}

// do sends a request through the contract's client, which holds the
// response to openapi.yaml.
func do(t *testing.T, method, url string) reply {
	t.Helper()

	return send(t, contract.Client(), request(t, method, url))
}

// getWith sends a GET with the headers given as name, value pairs.
func getWith(t *testing.T, url string, header ...string) reply {
	t.Helper()

	req := request(t, http.MethodGet, url)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}

	return send(t, contract.Client(), req)
}

// admin sends a GET to the admin port, which openapi.yaml doesn't cover.
func admin(t *testing.T, url string) reply {
	t.Helper()

	return send(t, http.DefaultClient, request(t, http.MethodGet, url))
}

func request(t *testing.T, method, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)

	return req
}

// send sends req through c and reads the whole response.
func send(t *testing.T, c *http.Client, req *http.Request) reply {
	t.Helper()

	resp, err := c.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return reply{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// requestLine waits for the request line of path in p's log. The log
// comes through a pipe, so a line can arrive after the response it
// describes.
func requestLine(t *testing.T, p *harness.Process, path string) map[string]any {
	t.Helper()

	var line map[string]any

	require.Eventually(t, func() bool {
		for _, l := range logLines(t, p.Log(), "request") {
			if l["path"] == path {
				line = l
				return true
			}
		}

		return false
	}, 5*time.Second, 10*time.Millisecond, "no request line for %s", path)

	return line
}

// logLines returns the JSON log lines whose msg is msg.
func logLines(t *testing.T, log, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any

	lines := bufio.NewScanner(strings.NewReader(log))
	for lines.Scan() {
		var line map[string]any
		if json.Unmarshal(lines.Bytes(), &line) == nil && line["msg"] == msg {
			out = append(out, line)
		}
	}

	return out
}
