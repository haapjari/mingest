package dataset_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

func TestWriteFile_RoundTripsArchive(t *testing.T) {
	fixture := dataset.Fixture()

	data := readURL(t, dataset.WriteFile(t, fixture))

	assert.Equal(t, fixture.Data, data)
	assert.Len(
		t,
		readArchive(t, bytes.NewReader(data)),
		dataset.FixtureMailFiles,
	)
}

func TestReplace_KeepsURLAndChangesArchive(t *testing.T) {
	rawURL := dataset.WriteFile(t, dataset.Fixture())
	other := &dataset.Memory{Data: []byte("another archive")}

	dataset.Replace(t, rawURL, other)

	assert.Equal(t, other.Data, readURL(t, rawURL))
}

func TestTruncated_StopsAfterNBytes(t *testing.T) {
	fixture := dataset.Fixture()
	half := fixture.Size / 2

	var buf bytes.Buffer

	n, err := dataset.Truncated(fixture, half).WriteTo(&buf)
	require.NoError(t, err)

	assert.Equal(t, half, n)
	assert.Equal(t, fixture.Data[:half], buf.Bytes())
	assert.ErrorIs(t, drain(&buf), io.ErrUnexpectedEOF)
}

func TestTruncated_PassesShortDatasetWhole(t *testing.T) {
	short := &dataset.Memory{Data: []byte("short")}

	var buf bytes.Buffer

	n, err := dataset.Truncated(short, 100).WriteTo(&buf)
	require.NoError(t, err)

	assert.Equal(t, int64(5), n)
	assert.Equal(t, "short", buf.String())
}

// readURL parses a file:// URL, asserts the scheme, and returns the contents
// of the file it points to. The tests use it to read back the archive that a
// dataset call wrote to disk.
func readURL(t *testing.T, rawURL string) []byte {
	t.Helper()

	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	require.Equal(t, "file", u.Scheme)

	data, err := os.ReadFile(u.Path)
	require.NoError(t, err)

	return data
}

// drain decompresses a tar.gz stream to its end and returns the first error.
// A complete archive drains cleanly (nil). A truncated one, such as the output
// of dataset.Truncated, fails with io.ErrUnexpectedEOF because gzip never
// reaches its trailer. The tests use this to check whether the stream is intact.
func drain(r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}

	_, err = io.ReadAll(gz)

	return err
}
