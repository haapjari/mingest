package dataset

import (
	"archive/tar"
	"compress/gzip"
	"io"
)

// archive writes Mail files into a tar.gz one at a time, without holding
// them all in memory.
//
// Errors are sticky, as in bufio.Writer: the first failure is kept in err,
// and close returns it.
type archive struct {
	tw  *tar.Writer
	gz  *gzip.Writer
	err error
}

func newArchive(w io.Writer) *archive {
	// BestSpeed is a valid level, so NewWriterLevel can't fail.
	gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)

	return &archive{tw: tar.NewWriter(gz), gz: gz}
}

// add writes one Mail file.
func (a *archive) add(name string, data []byte) {
	header := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(data)),
	}

	a.keep(a.tw.WriteHeader(header))

	_, err := a.tw.Write(data)
	a.keep(err)
}

// close finishes the archive and returns the first error of any add or of
// closing.
func (a *archive) close() error {
	a.keep(a.tw.Close())
	a.keep(a.gz.Close())

	return a.err
}

// keep stores err unless an earlier error is already stored.
func (a *archive) keep(err error) {
	if a.err == nil {
		a.err = err
	}
}
