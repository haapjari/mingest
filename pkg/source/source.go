package source

import (
	"context"
	"io"
)

// Source opens a Dataset for reading.
type Source interface {
	Open(ctx context.Context) (*Stream, error)
}

// Stream is an open Dataset.
type Stream struct {
	io.ReadCloser

	URL string

	// Size is the Dataset's length in bytes, or -1 when unknown.
	Size int64

	// Version changes with the Dataset under URL, or is "" if unknown.
	Version string
}
