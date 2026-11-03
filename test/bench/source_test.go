package bench_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/bench"
	"mingest/test/dataset"
)

func TestSource_ServesArchiveWithLengthAndETag(t *testing.T) {
	t.Parallel()

	d := dataset.Generate(dataset.Params{
		Seed: 1, MailFiles: 20, CopyRatio: 2, Senders: 5,
		BodyMin: 10, BodyMax: 20,
	})

	var want bytes.Buffer
	_, err := d.WriteTo(&want)
	require.NoError(t, err)

	s, err := bench.NewSource(d, "v1")
	require.NoError(t, err)
	assert.Equal(t, int64(want.Len()), s.Size())

	rec := httptest.NewRecorder()
	s.ServeHTTP(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	assert.Equal(t, `"v1"`, rec.Header().Get("ETag"))
	assert.Equal(
		t,
		[]string{
			strconv.Itoa(want.Len()),
		},
		rec.Header().Values("Content-Length"),
	)
	assert.Equal(t, want.Bytes(), rec.Body.Bytes())
}

func TestSource_ServesTheSameBytesEveryTime(t *testing.T) {
	t.Parallel()

	s, err := bench.NewSource(dataset.Generate(dataset.Params{
		Seed: 2, MailFiles: 10, CopyRatio: 1, Senders: 3,
		BodyMin: 5, BodyMax: 9,
	}), "v")
	require.NoError(t, err)

	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	s.ServeHTTP(
		first,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
	)
	s.ServeHTTP(
		second,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
	)

	assert.Equal(t, first.Body.Bytes(), second.Body.Bytes())
}

func TestNewSource_FailsWhenDatasetCannotBeWritten(t *testing.T) {
	t.Parallel()

	_, err := bench.NewSource(failing{}, "v")

	require.ErrorIs(t, err, errWrite)
}

var errWrite = errors.New("write failed")

type failing struct{}

func (failing) WriteTo(io.Writer) (int64, error) { return 0, errWrite }
