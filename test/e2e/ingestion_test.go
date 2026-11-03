//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
	"mingest/test/harness"
)

func TestStart_IngestsTheFixture(t *testing.T) {
	t.Parallel()

	p := start(t, serveSlowly(t, dataset.Fixture(), fixtureStep).env...)

	first := do(t, http.MethodPost, p.URL+"/start")
	require.Equal(t, http.StatusAccepted, first.status)
	assert.Equal(t, "/status", first.header.Get("Location"))

	begun := statusOf(t, first)
	assert.Equal(t, "running", begun.State)

	second := do(t, http.MethodPost, p.URL+"/start")
	require.Equal(t, http.StatusConflict, second.status)

	running, ok := second.problem(t)["ingestion"].(map[string]any)
	require.True(t, ok, "the 409 holds the running Ingestion")
	assert.Equal(t, begun.IngestionID, running["ingestion_id"])

	done := waitEnded(t, p.URL)
	assert.Equal(t, "done", done.State)
	assert.Equal(t, int64(dataset.FixtureMailFiles), done.MailFiles)
	assert.Equal(t, int64(dataset.FixtureMessages), done.Messages)
	assert.Equal(t, int64(dataset.FixtureSenders), done.Senders)
	assert.Equal(t, int64(dataset.FixtureWithoutSender), done.WithoutSender)
	assert.Contains(t, get(t, p.URL+"/status").body,
		`"progress_percent":100.0`)

	again := statusOf(t, do(t, http.MethodPost, p.URL+"/start"))
	assert.NotEqual(t, begun.IngestionID, again.IngestionID)
	assert.Zero(t, again.MailFiles, "the counts start again from zero")
}

func TestStatus_NegotiatesAndForbidsCaching(t *testing.T) {
	t.Parallel()

	p := start(t)

	html := getWith(t, p.URL+"/status", "Accept", "text/html")
	assert.Equal(t, http.StatusNotAcceptable, html.status)
	assert.Equal(t, "Accept", html.header.Get("Vary"))

	json := get(t, p.URL+"/status")
	assert.Equal(t, http.StatusOK, json.status)
	assert.Equal(t, "no-store", json.header.Get("Cache-Control"))
	assert.Equal(t, "Accept", json.header.Get("Vary"))

	for _, req := range []struct{ method, path, allow string }{
		{http.MethodPut, "/status", "GET"},
		{http.MethodGet, "/start", "POST"},
		{http.MethodHead, "/top-senders", "GET"},
	} {
		r := do(t, req.method, p.URL+req.path)
		assert.Equal(t, http.StatusMethodNotAllowed, r.status)
		assert.Equal(t, req.allow, r.header.Get("Allow"))
	}
}

func TestRun_ResumesAfterSIGKILL(t *testing.T) {
	t.Parallel()

	archive := dataset.Generate(generated(1))
	src := serveSlowly(t, archive, generatedStep)
	env := append([]string{"MINGEST_DATA_DIR=" + t.TempDir()}, src.env...)

	killed := start(t, env...)
	begun := statusOf(t, do(t, http.MethodPost, killed.URL+"/start"))
	waitCommitted(t, killed.URL)
	killed.Kill(t)

	resumed := start(t, env...)
	end := waitEnded(t, resumed.URL)

	plan := archive.Plan()
	assert.Equal(t, "done", end.State)
	assert.Equal(t, begun.IngestionID, end.IngestionID)
	assert.Equal(t, int64(1), end.Resumes)
	assert.Equal(t, int64(plan.MailFiles), end.MailFiles)
	assert.Equal(t, int64(plan.Messages), end.Messages)
	assert.Equal(t, int64(plan.Senders), end.Senders)
	assert.Equal(t, plan.TopSenders,
		topOf(t, get(t, resumed.URL+"/top-senders")).Senders,
		"the Top Senders of an uninterrupted run")
}

func TestRun_FailsResumeOnChangedSource(t *testing.T) {
	t.Parallel()

	src := serveSlowly(t, dataset.Generate(generated(1)), generatedStep)
	env := append([]string{"MINGEST_DATA_DIR=" + t.TempDir()}, src.env...)

	killed := start(t, env...)
	do(t, http.MethodPost, killed.URL+"/start")
	waitCommitted(t, killed.URL)
	killed.Kill(t)

	src.replace(t, dataset.Generate(generated(2)))
	end := waitEnded(t, start(t, env...).URL)

	assert.Equal(t, "failed", end.State)
	require.NotNil(t, end.Error)
	assert.Equal(t, "source_changed", end.Error.Code)
}

func TestStart_FailsOnCorruptArchive(t *testing.T) {
	t.Parallel()

	fixture := dataset.Fixture()
	url := dataset.WriteFile(t, dataset.Truncated(fixture, fixture.Size/2))
	p := start(t, "MINGEST_DATASET_SOURCE="+url)

	do(t, http.MethodPost, p.URL+"/start")
	end := waitEnded(t, p.URL)

	assert.Equal(t, "failed", end.State)
	require.NotNil(t, end.Error)
	assert.Equal(t, "archive_corrupt", end.Error.Code)
}

// How slowly a slowSource serves: one step, then a pause. The fixture
// takes about a second, the generated archive about two.
const (
	fixtureStep   = 256
	generatedStep = 8 * 1024
	pause         = 50 * time.Millisecond
)

// ingestTimeout is how long a test waits for an Ingestion.
const ingestTimeout = 60 * time.Second

// generated is a dataset of 5,000 Mail files, about 270 KB, which commits
// several chunks.
func generated(seed uint64) dataset.Params {
	return dataset.Params{
		Seed:      seed,
		MailFiles: 5000,
		CopyRatio: 2.03,
		Senders:   200,
		BodyMin:   10,
		BodyMax:   200,
	}
}

func statusOf(t *testing.T, r reply) harness.Status {
	t.Helper()

	var s harness.Status
	require.NoError(t, json.Unmarshal([]byte(r.body), &s))

	return s
}

// waitEnded polls GET /status until the Ingestion is done or failed.
func waitEnded(t *testing.T, url string) harness.Status {
	t.Helper()

	return harness.WaitStatus(t, contract.Client(), url, ingestTimeout,
		harness.Status.Ended)
}

// waitCommitted polls GET /status until a running Ingestion has committed
// a chunk.
func waitCommitted(t *testing.T, url string) {
	t.Helper()

	s := harness.WaitStatus(t, contract.Client(), url, ingestTimeout,
		func(s harness.Status) bool { return s.MailFiles > 0 })
	require.Equal(t, "running", s.State,
		"the archive was ingested before the test could act")
}

// slowSource serves an archive over HTTPS a step at a time, so an
// Ingestion of it is still running when the test acts. Its ETag is the
// archive's hash, so replace changes the version a resume checks.
type slowSource struct {
	step    int
	archive atomic.Pointer[[]byte]

	// env points mingest at the source and makes it trust its certificate.
	env []string
}

func serveSlowly(t *testing.T, d io.WriterTo, step int) *slowSource {
	t.Helper()

	s := &slowSource{step: step}
	s.replace(t, d)

	s.env = harness.ServeTLS(t, http.HandlerFunc(s.serve))

	return s
}

// replace serves the given Dataset from the next request on.
func (s *slowSource) replace(t *testing.T, d io.WriterTo) {
	t.Helper()

	var b bytes.Buffer
	_, err := d.WriteTo(&b)
	require.NoError(t, err)

	archive := b.Bytes()
	s.archive.Store(&archive)
}

func (s *slowSource) serve(w http.ResponseWriter, r *http.Request) {
	archive := *s.archive.Load()
	sum := sha256.Sum256(archive)

	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))

	for part := range slices.Chunk(archive, s.step) {
		if write(w, part) != nil || !paused(r.Context()) {
			return
		}
	}
}

// write sends part to the client at once.
func write(w http.ResponseWriter, part []byte) error {
	if _, err := w.Write(part); err != nil {
		return err
	}

	return http.NewResponseController(w).Flush()
}

// paused waits out a pause, and reports false if the client left first.
func paused(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(pause):
		return true
	}
}
