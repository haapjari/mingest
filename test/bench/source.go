package bench

import (
	"io"
	"net/http"
	"strconv"
)

// Source serves a Dataset as a tar.gz over HTTP. The archive is generated
// again for every request and streamed, never held, so a Dataset of any
// size costs the test no memory. Its size is counted once, up front, by
// generating it into io.Discard.
type Source struct {
	d    io.WriterTo
	size int64
	etag string
}

// NewSource counts d's bytes and returns its Source. version is the strong
// ETag, unquoted: the same version must mean the same bytes.
func NewSource(d io.WriterTo, version string) (*Source, error) {
	n, err := d.WriteTo(io.Discard)
	if err != nil {
		return nil, err
	}

	return &Source{d: d, size: n, etag: strconv.Quote(version)}, nil
}

// Size is the archive's length in bytes.
func (s *Source) Size() int64 { return s.size }

// ServeHTTP sends the whole archive with its length and ETag. A client that
// leaves mid-way ends the write.
func (s *Source) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(s.size, 10))
	w.Header().Set("ETag", s.etag)

	_, _ = s.d.WriteTo(w)
}
