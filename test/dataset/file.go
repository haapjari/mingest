package dataset

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

var errLimit = errors.New("dataset: truncated")

// WriteFile writes the Dataset as a file in t.TempDir() and returns its
// file:// URL.
func WriteFile(t testing.TB, d io.WriterTo) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dataset.tar.gz")
	write(t, path, d)

	return (&url.URL{Scheme: "file", Path: path}).String()
}

// Replace overwrites the file at a WriteFile URL with the Dataset.
func Replace(t testing.TB, rawURL string, d io.WriterTo) {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}

	write(t, u.Path, d)
}

// Truncated returns the Dataset cut after n bytes, a corrupt archive. It
// writes nothing past n.
func Truncated(d io.WriterTo, n int64) io.WriterTo {
	return truncated{d: d, n: n}
}

type truncated struct {
	d io.WriterTo
	n int64
}

// WriteTo writes the Dataset to w, stopping after t.n bytes. Hitting the
// limit is the intended outcome, so errLimit is swallowed and the write
// reports success with the byte count actually written. Any other error from
// the Dataset is returned.
func (t truncated) WriteTo(w io.Writer) (int64, error) {
	lw := &limitWriter{w: w, left: t.n}

	_, err := t.d.WriteTo(lw)
	if errors.Is(err, errLimit) {
		err = nil
	}

	return t.n - lw.left, err
}

// limitWriter forwards up to left bytes to w, then fails with errLimit.
// A write crossing the limit is cut short at left and returns errLimit.
type limitWriter struct {
	w    io.Writer
	left int64
}

// Write forwards at most l.left bytes of p. If p exceeds the budget, the
// leading bytes are written and errLimit is returned, unless the underlying
// write failed first.
func (l *limitWriter) Write(p []byte) (int, error) {
	short := int64(len(p)) > l.left
	if short {
		p = p[:l.left]
	}

	n, err := l.w.Write(p)
	l.left -= int64(n)

	if err == nil && short {
		err = errLimit
	}

	return n, err
}

func write(t testing.TB, path string, d io.WriterTo) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = d.WriteTo(f)
	if err = errors.Join(err, f.Close()); err != nil {
		t.Fatal(err)
	}
}
