//go:build bench

package bench_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/bench"
	"mingest/test/dataset"
	"mingest/test/harness"
)

// defaultBase is the 1× Dataset's Mail files when TEST_BENCH_BASE is unset.
const defaultBase = 50_000

// ingestTimeout bounds one run, the 100× one included.
const ingestTimeout = 30 * time.Minute

func TestMemory_StaysFlat(t *testing.T) {
	base := baseMailFiles(t)

	var runs []bench.Result
	for _, scale := range []int{1, 10, 100} {
		r := ingest(t, scale, params(scale*base))
		t.Logf("%d×: %d Mail files in %s, peak RSS %.1f MiB",
			scale, r.MailFiles, r.Duration.Round(time.Second),
			float64(r.PeakRSS)/(1<<20))

		runs = append(runs, r)
	}

	// 1× is a warm-up; 100× is held to 10×.
	require.NoError(t, bench.Flat(runs[1], runs[2:]))
}

func baseMailFiles(t *testing.T) int {
	t.Helper()

	s := os.Getenv("TEST_BENCH_BASE")
	if s == "" {
		return defaultBase
	}

	n, err := strconv.Atoi(s)
	require.NoError(t, err, "TEST_BENCH_BASE")
	require.Positive(t, n, "TEST_BENCH_BASE")

	return n
}

// params shape a Dataset like Enron's: about two Copies per Message, a
// skewed set of Senders, bodies of a few hundred bytes to a few KB. The
// Senders stay the same at every scale, so only the Dataset's size grows.
func params(mailFiles int) dataset.Params {
	return dataset.Params{
		Seed:      1,
		MailFiles: mailFiles,
		CopyRatio: 2.03,
		Senders:   20_000,
		BodyMin:   200,
		BodyMax:   3000,
	}
}

// ingest runs the binary over the Dataset p describes until done, and
// checks the counts and Top Senders against the generator's plan.
func ingest(t *testing.T, scale int, p dataset.Params) bench.Result {
	t.Helper()

	d := dataset.Generate(p)
	plan := d.Plan()

	proc := harness.Start(t, bin, source(t, d, p)...)

	require.Equal(t, http.StatusAccepted, harness.Send(t,
		http.DefaultClient, http.MethodPost, proc.URL+"/start", nil))

	began := time.Now()
	s := harness.WaitStatus(t, http.DefaultClient, proc.URL, ingestTimeout,
		harness.Status.Ended)
	took := time.Since(began)

	require.Equal(t, "done", s.State, proc.Log())
	checkPlan(t, proc.URL, s, plan)
	proc.Stop(t)

	return bench.Result{
		Scale:     scale,
		MailFiles: plan.MailFiles,
		Duration:  took,
		PeakRSS:   bench.PeakRSS(proc.State()),
	}
}

func checkPlan(t *testing.T, url string, s harness.Status, plan dataset.Plan) {
	t.Helper()

	assert.Equal(t, int64(plan.MailFiles), s.MailFiles)
	assert.Equal(t, int64(plan.Messages), s.Messages)
	assert.Equal(t, int64(plan.Senders), s.Senders)

	var top harness.TopSenders
	require.Equal(t, http.StatusOK, harness.Send(t,
		http.DefaultClient, http.MethodGet, url+"/top-senders", &top))
	assert.Equal(t, plan.TopSenders, top.Senders)
}

// source serves d to the binary and returns the environment that points it
// there: HTTPS on Linux, a file on macOS or with -file.
func source(t *testing.T, d *dataset.Synthetic, p dataset.Params) []string {
	t.Helper()

	if *fileDataset {
		return fileSource(t, d)
	}

	return httpsSource(t, d, version(p))
}

// version is a strong ETag for the Dataset p describes: the same Params
// always give the same bytes.
func version(p dataset.Params) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%+v", p))

	return hex.EncodeToString(sum[:])
}

func httpsSource(t *testing.T, d io.WriterTo, version string) []string {
	t.Helper()

	src, err := bench.NewSource(d, version)
	require.NoError(t, err)

	return harness.ServeTLS(t, src)
}

// fileSource writes d to the test's temporary directory, which is removed
// when the test ends.
func fileSource(t *testing.T, d io.WriterTo) []string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dataset.tar.gz")

	f, err := os.Create(path)
	require.NoError(t, err)

	_, err = d.WriteTo(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	return []string{"MINGEST_DATASET_SOURCE=file://" + path}
}
