package ingest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

var t0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func TestChunkReader_Next_CutsAtChunkMailFiles(t *testing.T) {
	t.Parallel()

	chunks, err := collect(fixture(), 0, options(5))
	require.NoError(t, err)

	assert.Equal(t, []int{5, 5, 5, 5, 2}, lengths(chunks))
}

func TestChunkReader_Next_CutsAtChunkBytes(t *testing.T) {
	t.Parallel()

	o := options(dataset.FixtureMailFiles)
	o.ChunkBytes = 1024

	chunks, err := collect(fixture(), 0, o)
	require.NoError(t, err)

	assert.Len(t, paths(chunks), dataset.FixtureMailFiles)
	assert.Greater(t, len(chunks), 1)

	for _, c := range chunks {
		assert.True(t, len(c.files) == 1 || chunkBytes(c) <= o.ChunkBytes)
	}
}

func TestChunkReader_Next_GivesLargeMailFileOwnChunk(t *testing.T) {
	t.Parallel()

	large := "From: " + strings.Repeat("x", 2*1024) + "\n\nbody\n"
	data := archive(t,
		entry{name: "a", body: "From: a@example.com\n\nbody\n"},
		entry{name: "large", body: large},
		entry{name: "b", body: "From: b@example.com\n\nbody\n"},
	)

	o := options(10)
	o.ChunkBytes = 1024

	chunks, err := collect(data, 0, o)
	require.NoError(t, err)

	assert.Equal(t, [][]string{{"a"}, {"large"}, {"b"}}, chunkPaths(chunks))
}

func TestChunkReader_Next_CutsAtChunkAge(t *testing.T) {
	t.Parallel()

	// gunzip hands on up to 32 KiB at a time, so the Mail files must not
	// compress for them to arrive spread out.
	data := archive(t, incompressible(40, 8*1024)...)

	synctest.Test(t, func(t *testing.T) {
		o := options(40)
		o.ChunkAge = time.Second
		o.ReadBuffer = 1024

		r := &slowReader{r: bytes.NewReader(data), wait: 100 * time.Millisecond}

		chunks, err := collectFrom(r, 0, o)
		require.NoError(t, err)

		assert.Len(t, paths(chunks), 40)
		assert.Greater(t, len(chunks), 1)
	})
}

func TestChunkReader_Next_CountsRegularFilesOnly(t *testing.T) {
	t.Parallel()

	data := archive(t,
		entry{name: "maildir/", typ: tar.TypeDir},
		entry{name: "maildir/a", body: "From: a@example.com\n\n"},
		entry{name: "maildir/link", typ: tar.TypeSymlink},
		entry{name: "maildir/b", body: "From: b@example.com\n\n"},
	)

	chunks, err := collect(data, 0, options(10))
	require.NoError(t, err)
	assert.Equal(t, []string{"maildir/a", "maildir/b"}, paths(chunks))

	chunks, err = collect(data, 1, options(10))
	require.NoError(t, err)
	assert.Equal(t, []string{"maildir/b"}, paths(chunks))
}

func TestChunkReader_Next_SkipsToSameChunksAsFullRun(t *testing.T) {
	t.Parallel()

	full, err := collect(fixture(), 0, options(1))
	require.NoError(t, err)
	require.Len(t, full, dataset.FixtureMailFiles)

	for n := range dataset.FixtureMailFiles {
		chunks, err := collect(fixture(), int64(n), options(1))
		require.NoError(t, err)
		assert.Equal(t, full[n:], chunks, "skip %d", n)
	}
}

func TestChunkReader_Next_EndsWithCheckpointWhenAllSkipped(t *testing.T) {
	t.Parallel()

	data := fixture()

	chunks, err := collect(data, dataset.FixtureMailFiles, options(1))
	require.NoError(t, err)

	require.Len(t, chunks, 1)
	assert.Empty(t, chunks[0].files)
	assert.Equal(t, int64(len(data)), chunks[0].bytesRead)
}

func TestChunkReader_Next_EndsWithCheckpointWithoutMailFiles(t *testing.T) {
	t.Parallel()

	data := archive(t, entry{name: "maildir/", typ: tar.TypeDir})

	chunks, err := collect(data, 0, options(1))
	require.NoError(t, err)

	require.Len(t, chunks, 1)
	assert.Empty(t, chunks[0].files)
	assert.Equal(t, int64(len(data)), chunks[0].bytesRead)
}

func TestChunkReader_Next_RejectsTruncatedArchiveAsCorrupt(t *testing.T) {
	t.Parallel()

	data := fixture()

	for n := range len(data) {
		_, err := collect(data[:n], 0, options(1))
		require.ErrorIs(t, err, errArchiveCorrupt, "cut at %d", n)
		assert.NotErrorIs(t, err, errSource, "cut at %d", n)
	}
}

func TestChunkReader_Next_RejectsWrongCRCAtEnd(t *testing.T) {
	t.Parallel()

	data := fixture()

	// A gzip file ends with an 8-byte trailer: a checksum (CRC32) of the
	// original data, then the original length. Readers check it only after
	// all the data has been decompressed.
	//
	// Flipping one byte of the checksum leaves the archive contents intact,
	// so every file still decompresses fine. Only the final check fails.
	data[len(data)-8] ^= 0xff

	chunks, err := collect(data, 0, options(1))
	require.ErrorIs(t, err, errArchiveCorrupt)

	// The reader holds back the last chunk until the checksum has been
	// verified, so corrupt data is never handed out. That is why exactly
	// one file (the last) is missing here.
	assert.Len(t, chunks, dataset.FixtureMailFiles-1)
}

func TestChunkReader_Next_TellsSourceErrorsFromCorruption(t *testing.T) {
	t.Parallel()

	data := fixture()

	for n := range len(data) + 1 {
		r := &failingReader{
			r:   bytes.NewReader(data[:n]),
			err: io.ErrUnexpectedEOF,
		}

		_, err := collectFrom(r, 0, options(1))
		require.ErrorIs(t, err, errSource, "failed at %d", n)
		assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "failed at %d", n)
		assert.NotErrorIs(t, err, errArchiveCorrupt, "failed at %d", n)
	}
}

func TestChunkReader_Next_KeepsSourceErrorCause(t *testing.T) {
	t.Parallel()

	data := fixture()
	cause := errors.New("connection reset")
	r := &failingReader{r: bytes.NewReader(data[:len(data)/2]), err: cause}

	_, err := collectFrom(r, 0, options(1))
	require.ErrorIs(t, err, errSource)
	assert.ErrorIs(t, err, cause)
}

func TestChunkReader_Next_CountsBytesReadUpToArchiveSize(t *testing.T) {
	t.Parallel()

	data := fixture()
	o := options(1)
	o.ReadBuffer = 512

	chunks, err := collect(data, 0, o)
	require.NoError(t, err)

	read := bytesRead(chunks)
	assert.IsNonDecreasing(t, read)
	assert.Equal(t, int64(len(data)), read[len(read)-1])
}

func TestChunkReader_Next_StopsWhenContextEnds(t *testing.T) {
	t.Parallel()

	rd, err := newChunkReader(bytes.NewReader(fixture()), 0, options(1))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	_, err = rd.Next(ctx)
	require.NoError(t, err)

	cancel()

	_, err = rd.Next(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func FuzzCut(f *testing.F) {
	f.Add([]byte{10, 0, 200, 5, 10, 0}, uint8(2), uint16(100), uint16(10))
	f.Add([]byte{255, 255, 0, 0, 1, 1}, uint8(0), uint16(0), uint16(0))

	f.Fuzz(func(t *testing.T, data []byte, files uint8, size, age uint16) {
		o := Options{
			ChunkMailFiles: int(files),
			ChunkBytes:     int64(size),
			ChunkAge:       time.Duration(age) * time.Millisecond,
		}
		in := arrivals(data)

		got := cut(o, in)

		for _, c := range got {
			checkChunk(t, o, in, c)
		}

		assert.Equal(t, indices(len(in)), flatten(got))
	})
}

func FuzzReadArchive(f *testing.F) {
	data := fixture()
	f.Add(data)
	f.Add(data[:len(data)/2])
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := collect(data, 0, options(3))
		if err != nil {
			require.ErrorIs(t, err, errArchiveCorrupt)
		}
	})
}

// fixture returns a fresh copy of the fixture archive.
func fixture() []byte {
	return dataset.Fixture().Data
}

// options returns bounds that cut only at n Mail files.
func options(n int) Options {
	return Options{
		ChunkMailFiles: n,
		ChunkBytes:     4 * 1024 * 1024,
		ChunkAge:       time.Hour,
		ReadBuffer:     1024 * 1024,
	}
}

func collect(data []byte, skip int64, o Options) ([]rawChunk, error) {
	return collectFrom(bytes.NewReader(data), skip, o)
}

// collectFrom reads r to its end, and returns every chunk it cut with
// the error that ended it, nil at the end of the archive.
func collectFrom(r io.Reader, skip int64, o Options) ([]rawChunk, error) {
	chunks := []rawChunk{}

	rd, err := newChunkReader(r, skip, o)
	if err != nil {
		return chunks, err
	}

	for {
		c, err := rd.Next(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				return chunks, nil
			}

			return chunks, err
		}

		chunks = append(chunks, c)
	}
}

func lengths(chunks []rawChunk) []int {
	out := make([]int, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, len(c.files))
	}

	return out
}

func paths(chunks []rawChunk) []string {
	var out []string
	for _, c := range chunkPaths(chunks) {
		out = append(out, c...)
	}

	return out
}

func chunkPaths(chunks []rawChunk) [][]string {
	out := make([][]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, filePaths(c.files))
	}

	return out
}

func filePaths(files []rawFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.path)
	}

	return out
}

func bytesRead(chunks []rawChunk) []int64 {
	out := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.bytesRead)
	}

	return out
}

func chunkBytes(c rawChunk) int64 {
	var n int64
	for _, f := range c.files {
		n += fileSize(f)
	}

	return n
}

type entry struct {
	name string
	typ  byte
	body string
}

// archive writes entries into a tar.gz. An entry with no entry.typ is a
// regular file.
func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		require.NoError(t, tw.WriteHeader(header(e)))

		_, err := tw.Write([]byte(e.body))
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	return buf.Bytes()
}

// incompressible returns n Mail files with random bodies of size bytes.
func incompressible(n, size int) []entry {
	rng := rand.NewChaCha8([32]byte{})
	body := make([]byte, size)

	out := make([]entry, 0, n)
	for i := range n {
		_, _ = rng.Read(body)
		out = append(out, entry{
			name: fmt.Sprintf("mail/%d", i),
			body: "From: a@example.com\n\n" + string(body),
		})
	}

	return out
}

func header(e entry) *tar.Header {
	h := &tar.Header{
		Typeflag: e.typ,
		Name:     e.name,
		Mode:     0o644,
		Size:     int64(len(e.body)),
	}
	if h.Typeflag == 0 {
		h.Typeflag = tar.TypeReg
	}

	return h
}

// arrival is a Mail file and when it reaches the cutter.
type arrival struct {
	file rawFile
	at   time.Time
}

// cut runs the cutter over in and returns each chunk as the indices of
// its Mail files, which it writes into their digests.
func cut(o Options, in []arrival) [][]uint64 {
	c := cutter{o: o}

	var (
		out [][]uint64
		i   uint64
	)
	for _, a := range in {
		binary.BigEndian.PutUint64(a.file.scanned.Body[:], i)
		i++

		if chunk, ok := c.add(a.file, a.at); ok {
			out = append(out, fileIndices(chunk))
		}
	}

	if chunk, ok := c.flush(); ok {
		out = append(out, fileIndices(chunk))
	}

	return out
}

// arrivals decodes fuzz bytes as pairs of a Mail file's size and the
// milliseconds since the one before.
func arrivals(data []byte) []arrival {
	out := make([]arrival, 0, len(data)/2)
	at := t0

	for i := 0; i+1 < len(data); i += 2 {
		at = at.Add(time.Duration(data[i+1]) * time.Millisecond)
		out = append(out, arrival{fileOfSize(int(data[i]) * 4), at})
	}

	return out
}

// checkChunk checks that the chunk is within the bounds of the options, or
// holds one Mail file.
func checkChunk(t *testing.T, o Options, in []arrival, c []uint64) {
	t.Helper()

	require.NotEmpty(t, c)

	if len(c) == 1 {
		return
	}

	var size int64
	for _, i := range c {
		size += fileSize(in[i].file)
	}

	span := in[c[len(c)-1]].at.Sub(in[c[0]].at)

	assert.LessOrEqual(t, len(c), o.ChunkMailFiles)
	assert.LessOrEqual(t, size, o.ChunkBytes)
	assert.Less(t, span, o.ChunkAge)
}

// fileOfSize returns a Mail file of size bytes in a chunk, or of the
// digest's size if size is smaller.
func fileOfSize(size int) rawFile {
	return rawFile{path: strings.Repeat("x", max(size-sha256.Size, 0))}
}

// fileIndices reads back the indices that cut wrote into the digests.
func fileIndices(c rawChunk) []uint64 {
	out := make([]uint64, 0, len(c.files))
	for _, f := range c.files {
		out = append(out, binary.BigEndian.Uint64(f.scanned.Body[:]))
	}

	return out
}

func indices(n int) []uint64 {
	out := make([]uint64, 0, n)

	var i uint64
	for range n {
		out = append(out, i)
		i++
	}

	return out
}

func flatten(chunks [][]uint64) []uint64 {
	out := []uint64{}
	for _, c := range chunks {
		out = append(out, c...)
	}

	return out
}

// slowReader waits before each read, in a synctest bubble's fake time.
type slowReader struct {
	r    io.Reader
	wait time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.wait)

	return s.r.Read(p)
}

// failingReader reads r, then fails with err where r would end.
type failingReader struct {
	r   io.Reader
	err error
}

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		err = f.err
	}

	return n, err
}
