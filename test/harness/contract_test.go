package harness_test

import (
	"bufio"
	"context"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/harness"
)

func TestLoadContract_FailsOnMissingFile(t *testing.T) {
	_, err := harness.LoadContract(filepath.Join(t.TempDir(), "none.yaml"))

	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestLoadContract_FailsOnNonOpenAPIFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.yaml")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))

	_, err := harness.LoadContract(path)

	assert.Error(t, err)
}

func TestContract_Client_FailsWhenServerIsDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	err := get(t, newContract(t).Client(), down.URL+"/status")

	assert.ErrorContains(t, err, "connect")
}

func TestContract_Client_RejectsMissingRequiredField(t *testing.T) {
	body := strings.Replace(idleStatus, `"resumes":0,`, "", 1)
	url := stub(t, http.StatusOK, "application/json", body)

	err := get(t, newContract(t).Client(), url+"/status")

	assert.ErrorContains(t, err, "GET /status breaks the contract")
	assert.ErrorContains(t, err, "resumes")
}

func TestContract_Client_FailsOnCutBody(t *testing.T) {
	url := stubHeader(t, http.StatusOK, http.Header{
		"Content-Type":   {"application/json"},
		"Content-Length": {"1000"},
	}, idleStatus)

	err := get(t, newContract(t).Client(), url+"/status")

	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestContract_Client_RejectsUndocumentedStatus(t *testing.T) {
	url := stub(t, http.StatusTeapot, "application/json", idleStatus)

	err := get(t, newContract(t).Client(), url+"/status")

	assert.ErrorContains(t, err, "418")
}

func TestContract_Client_RejectsUnknownPathWithoutProblem(t *testing.T) {
	url := stub(t, http.StatusNotFound, "text/plain", "404 page not found")

	err := get(t, newContract(t).Client(), url+"/nope")

	assert.Error(t, err)
}

func TestContract_Client_RejectsUnknownPathWithout404(t *testing.T) {
	url := stub(t, http.StatusOK, "application/problem+json", notFound)

	err := get(t, newContract(t).Client(), url+"/nope")

	assert.ErrorContains(t, err, "want 404")
}

func TestContract_Client_RejectsUnknownPathWithBadProblem(t *testing.T) {
	url := stub(t, http.StatusNotFound, "application/problem+json", "{}")

	err := get(t, newContract(t).Client(), url+"/nope")

	assert.ErrorContains(t, err, "title")
}

func TestContract_Client_Rejects405WithoutAllow(t *testing.T) {
	url := stubHeader(t, http.StatusMethodNotAllowed, problemHeader(""),
		methodNotAllowed)

	err := do(t, newContract(t).Client(), http.MethodDelete, url+"/top-senders")

	assert.ErrorContains(t, err, "Allow")
}

func TestContract_Client_Rejects405WithBadProblem(t *testing.T) {
	url := stubHeader(t, http.StatusMethodNotAllowed, problemHeader("GET"),
		"{}")

	err := do(t, newContract(t).Client(), http.MethodDelete, url+"/top-senders")

	assert.ErrorContains(t, err, "title")
}

func TestContract_Client_RejectsUnlistedMethodWithout405(t *testing.T) {
	url := stub(t, http.StatusOK, "text/plain", "ok")

	err := do(t, newContract(t).Client(), http.MethodDelete, url+"/top-senders")

	assert.ErrorContains(t, err, "405")
}

func TestContract_Client_PassesHeadWith405(t *testing.T) {
	url := stubHeader(t, http.StatusMethodNotAllowed, problemHeader("GET"),
		methodNotAllowed)

	err := do(t, newContract(t).Client(), http.MethodHead, url+"/status")

	assert.NoError(t, err)
}

func TestContract_Client_HandsBackStreamUnread(t *testing.T) {
	url := openStream(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		url+"/status", nil)
	require.NoError(t, err)

	resp, err := newContract(t).Client().Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "retry: 3000\n", line)
}

const idleStatus = `{"ingestion_id":null,"state":"idle",` +
	`"mail_files_processed":0,"messages_processed":0,"senders":0,` +
	`"messages_without_sender":0,"bytes_read":0,"bytes_total":null,` +
	`"progress_percent":null,"resumes":0,"started_at":null,` +
	`"updated_at":null,"finished_at":null,"error":null}`

const notFound = `{"type":"about:blank","title":"Not Found",` +
	`"status":404,"detail":"No route matches /nope.",` +
	`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`

const methodNotAllowed = `{"type":"about:blank",` +
	`"title":"Method Not Allowed","status":405,` +
	`"detail":"The path doesn't allow this method.",` +
	`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`

func newContract(t *testing.T) *harness.Contract {
	t.Helper()

	c, err := harness.OpenAPI()
	require.NoError(t, err)

	return c
}

// stub serves every request with the given status, content type and body,
// and the headers GET /status documents.
func stub(t *testing.T, status int, contentType, body string) string {
	t.Helper()

	return stubHeader(t, status, http.Header{
		"Content-Type":  {contentType},
		"Cache-Control": {"no-store"},
		"Vary":          {"Accept"},
	}, body)
}

// stubHeader serves every request with the given status, headers and body.
func stubHeader(
	t *testing.T, status int, header http.Header, body string,
) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			maps.Copy(w.Header(), header)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		},
	))
	t.Cleanup(srv.Close)

	return srv.URL
}

// openStream serves GET /status as an event stream that sends its first
// line and stays open until the test ends.
func openStream(t *testing.T) string {
	t.Helper()

	end := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Vary", "Accept")
			_, _ = io.WriteString(w, "retry: 3000\n\n")
			_ = http.NewResponseController(w).Flush()
			<-end
		},
	))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(end) })

	return srv.URL
}

// problemHeader is a problem's header, with Allow set unless allow is "".
func problemHeader(allow string) http.Header {
	h := http.Header{"Content-Type": {"application/problem+json"}}
	if allow != "" {
		h.Set("Allow", allow)
	}

	return h
}

func get(t *testing.T, c *http.Client, url string) error {
	t.Helper()

	return do(t, c, http.MethodGet, url)
}

func do(t *testing.T, c *http.Client, method, url string) error {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

func request(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, url, nil,
	)
	require.NoError(t, err)

	return c.Do(req)
}
