package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
)

// errFileURL is a file:// URL that names no local file.
var errFileURL = errors.New("source: a file:// URL needs a path on localhost")

// file is the Source of a local file.
type file struct {
	url, path string
}

// newFile accepts a file URL with a path and no host other than
// localhost, as RFC 8089 allows.
func newFile(rawURL string, u *url.URL) (*file, error) {
	if u.Path == "" || (u.Host != "" && u.Host != "localhost") {
		return nil, fmt.Errorf("%w: %q", errFileURL, rawURL)
	}

	return &file{url: rawURL, path: u.Path}, nil
}

// Open opens the file. Its Version is its size and mtime, which change
// whenever the file is replaced.
func (f *file) Open(_ context.Context) (*Stream, error) {
	fd, err := os.Open(f.path)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}

	info, err := fd.Stat()
	if err != nil {
		return nil, fmt.Errorf("source: %w", errors.Join(err, fd.Close()))
	}

	return &Stream{
		ReadCloser: fd,
		URL:        f.url,
		Size:       info.Size(),
		Version: fmt.Sprintf(
			"%d-%d",
			info.Size(),
			info.ModTime().UnixNano(),
		),
	}, nil
}
