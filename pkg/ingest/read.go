package ingest

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/trace"

	"mingest/pkg/message"
)

var (
	// errSource marks an error from reading the Source, such as a dropped
	// connection. The Ingester retries these.
	errSource = errors.New("source")

	// errArchiveCorrupt marks any other gzip or tar error: the archive
	// itself is bad, and retrying won't help.
	errArchiveCorrupt = errors.New("archive corrupt")
)

// rawChunk is a group of Mail files that Read has scanned and Parse hasn't
// identified yet. A chunk is the unit of commit: each one becomes one
// transaction in the Store.
type rawChunk struct {
	files []rawFile

	// bytesRead is how many compressed bytes had been read from the Source
	// when the chunk was cut. It drives the progress percentage.
	bytesRead int64

	// seq numbers the chunks in the order they were cut. The Read stage
	// sets it.
	seq int64
}

// rawFile is one scanned Mail file, with its path in the archive for the
// log lines.
type rawFile struct {
	path    string
	scanned message.Scanned
}

// chunkReader reads the Dataset and groups its Mail files into chunks. Like
// tar.Reader it hands them out one at a time: call Next until it returns
// io.EOF.
type chunkReader struct {
	count   *sourceReader
	gz      *gzip.Reader
	tr      *tar.Reader
	scanner message.Scanner
	cut     cutter
	skip    int64

	// last is the bytesRead of the last chunk returned.
	last int64
}

// newChunkReader starts reading the Dataset from r. Its first skip Mail
// files are passed over without being scanned, because an earlier attempt
// committed them. Errors from r come back marked errSource, and every other
// gzip or tar error comes back marked errArchiveCorrupt.
func newChunkReader(r io.Reader, skip int64, o Options) (*chunkReader, error) {
	count := &sourceReader{r: r}

	buf := bufio.NewReaderSize(count, o.ReadBuffer)

	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, classify(err)
	}

	return &chunkReader{
		count: count,
		gz:    gz,
		tr:    tar.NewReader(gz),
		cut:   cutter{o: o},
		skip:  skip,
	}, nil
}

// Next returns the next chunk: when it is full, or at the end of the
// archive. After the last chunk it returns io.EOF. If ctx ends first, it
// returns ctx's error. Each tar entry that isn't a regular file is passed
// over, and so are the first skip Mail files.
func (rd *chunkReader) Next(ctx context.Context) (rawChunk, error) {
	for ctx.Err() == nil {
		hdr, err := rd.tr.Next()
		if err != nil {
			return rd.end(err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if rd.skip > 0 {
			rd.skip--

			continue
		}

		s, err := rd.scanner.Scan(rd.tr)
		if err != nil {
			return rawChunk{}, classify(err)
		}

		// A Mail file that didn't fit closes the chunk before it.
		if c, ok := rd.cut.add(
			rawFile{path: hdr.Name, scanned: s}, time.Now()); ok {
			return rd.stamp(c), nil
		}
	}

	return rawChunk{}, ctx.Err()
}

// end handles tar.Next's error. Any error but io.EOF is fatal.
//
// At io.EOF the tar data is finished, but the gzip stream isn't quite: end
// reads it to the end. That makes gzip check its CRC and length trailer, so
// a corrupt tail is caught, and it brings bytesRead up to the archive's
// full size. Then end returns the last chunk, and io.EOF on the call after.
//
// If the last chunk has no Mail files but bytes were read since the
// previous chunk, end returns it anyway, empty: committing it records that
// the whole archive was read, so a finished Ingestion shows 100% even when
// no Mail files were left to commit at the end.
func (rd *chunkReader) end(err error) (rawChunk, error) {
	if !errors.Is(err, io.EOF) {
		return rawChunk{}, classify(err)
	}

	if _, err := io.Copy(io.Discard, rd.gz); err != nil {
		return rawChunk{}, classify(err)
	}

	c, ok := rd.cut.flush()
	if !ok && rd.count.n <= rd.last {
		return rawChunk{}, io.EOF
	}

	return rd.stamp(c), nil
}

// stamp sets c's bytesRead to the bytes read so far.
func (rd *chunkReader) stamp(c rawChunk) rawChunk {
	c.bytesRead = rd.count.n
	rd.last = c.bytesRead

	return c
}

// classify passes Source errors through and marks every other error as
// errArchiveCorrupt.
func classify(err error) error {
	if errors.Is(err, errSource) {
		return err
	}

	return fmt.Errorf("%w: %w", errArchiveCorrupt, err)
}

// cutter collects Mail files into the current chunk, and decides when it
// is full.
type cutter struct {
	o     Options
	files []rawFile
	bytes int64
	first time.Time
}

// add puts a Mail file, which arrived at now, into the current chunk. If
// it doesn't fit, add first closes the current chunk and returns it, then
// starts a new one with the Mail file. So every chunk has at least one
// Mail file, even one larger than ChunkBytes.
//
// The current chunk is full when it already holds ChunkMailFiles files,
// when the file would take it past ChunkBytes, or when its first file
// arrived ChunkAge ago. The age is only checked as a file arrives, so no
// timer is needed. Flushing an empty chunk does nothing.
func (c *cutter) add(f rawFile, now time.Time) (rawChunk, bool) {
	size := fileSize(f)

	cut, ok := rawChunk{}, false
	if len(c.files) >= c.o.ChunkMailFiles ||
		c.bytes+size > c.o.ChunkBytes ||
		now.Sub(c.first) >= c.o.ChunkAge {
		cut, ok = c.flush()
	}

	if len(c.files) == 0 {
		c.first = now
	}

	c.files = append(c.files, f)
	c.bytes += size

	return cut, ok
}

// flush closes the current chunk and returns it, or false if it is empty.
func (c *cutter) flush() (rawChunk, bool) {
	if len(c.files) == 0 {
		return rawChunk{}, false
	}

	out := rawChunk{files: c.files}
	c.files, c.bytes = nil, 0

	return out, true
}

// fileSize estimates the memory a Mail file takes in a chunk: the header
// fields kept, the body digest and the path.
func fileSize(f rawFile) int64 {
	s := &f.scanned

	return int64(len(s.From) + len(s.Date) + len(s.Subject) + len(s.To) +
		len(s.Cc) + sha256.Size + len(f.path))
}

// sourceReader counts the bytes read from the Source, for bytesRead, and
// marks every error except io.EOF as errSource. gzip and tar pass reader
// errors up unchanged, so the mark survives, and the Ingester can tell a
// failing Source from a corrupt archive.
type sourceReader struct {
	r io.Reader
	n int64
}

func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.n += int64(n)

	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("%w: %w", errSource, err)
	}

	return n, err
}

// read is the Pipeline's Read and Parse steps. It cuts each chunk,
// numbers it, parses it, and sends it to Write. It closes parsed when it
// returns, which lets Write finish.
func (p *pipeline) read(ctx context.Context, r io.Reader, skip int64) error {
	defer close(p.parsed)

	rd, err := newChunkReader(r, skip, p.o)
	if err != nil {
		return err
	}

	last := time.Now()

	// seq numbers the chunks in the order Read cuts them.
	for seq := int64(0); ; seq++ {
		c, err := rd.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		c.seq = seq
		p.readSpan(ctx, c, last)
		last = time.Now()

		err = send(ctx, p.parsed, p.parseChunk(ctx, c))
		p.buffers()

		if err != nil {
			return err
		}
	}
}

// readSpan records the chunk's read span: the time since the previous
// chunk was cut.
func (p *pipeline) readSpan(ctx context.Context, c rawChunk, since time.Time) {
	_, span := p.tracer.Start(ctx, stageRead,
		trace.WithTimestamp(since), chunkAttributes(c.seq, len(c.files)))
	span.End()

	p.metrics.stage(stageRead, since)
}
