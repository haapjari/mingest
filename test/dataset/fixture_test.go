package dataset_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/txtar"

	"mingest/test/dataset"
)

func TestFixture_HoldsEveryMailFile(t *testing.T) {
	files := fixtureFiles(t)

	assert.Len(t, files, dataset.FixtureMailFiles)
	assert.Contains(t, files, "allen-p/inbox/1.")
	assert.Contains(t, files, "kean-s/sent/1.")
}

func TestFixture_KeepsMailFilesAsWritten(t *testing.T) {
	raw, err := os.ReadFile("testdata/fixture.txtar")
	require.NoError(t, err)

	files := fixtureFiles(t)
	for _, f := range txtar.Parse(raw).Files {
		assert.Equal(t, f.Data, files[f.Name], f.Name)
	}
}

func TestFixture_KeepsCRLF(t *testing.T) {
	files := fixtureFiles(t)

	assert.Contains(
		t,
		string(files["bass-e/sent_items/1."]),
		"Game tonight\r\n",
	)
	assert.NotContains(t, string(files["bass-e/sent/1."]), "\r")
}

func TestMemory_Open_ServesArchiveEachTime(t *testing.T) {
	fixture := dataset.Fixture()
	fixture.Size = 42
	fixture.Version = "v2"

	for range 2 {
		stream, err := fixture.Open(t.Context())
		require.NoError(t, err)

		assert.Equal(t, int64(42), stream.Size)
		assert.Equal(t, "v2", stream.Version)
		assert.Len(t, readArchive(t, stream), dataset.FixtureMailFiles)
		require.NoError(t, stream.Close())
	}
}

func TestFixture_SetsSizeToArchiveLength(t *testing.T) {
	fixture := dataset.Fixture()

	assert.Equal(t, int64(len(fixture.Data)), fixture.Size)
}

type mailFile struct {
	name string
	data []byte
}

// readArchive decodes a tar.gz the way a reader of the Dataset would.
func readArchive(t *testing.T, r io.Reader) []mailFile {
	t.Helper()

	gz, err := gzip.NewReader(r)
	require.NoError(t, err)

	var files []mailFile

	tarReader := tar.NewReader(gz)

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return files
		}

		require.NoError(t, err)

		data, err := io.ReadAll(tarReader)
		require.NoError(t, err)

		files = append(files, mailFile{name: header.Name, data: data})
	}
}

func fixtureFiles(t *testing.T) map[string][]byte {
	t.Helper()

	var buf bytes.Buffer

	_, err := dataset.Fixture().WriteTo(&buf)
	require.NoError(t, err)

	files := map[string][]byte{}
	for _, f := range readArchive(t, &buf) {
		files[f.name] = f.data
	}

	return files
}

func lineWithPrefix(s, prefix string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}

	return ""
}
