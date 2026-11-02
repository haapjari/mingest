package message

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"iter"
	"math/bits"
	"strings"
)

// FieldCap is the most bytes kept of each header field. It is part of the
// Message rules, since a truncated field changes the fingerprint.
const FieldCap = 128 * 1024

// bufferSize is the bufio.Reader's size. A longer line is read in
// fragments, never whole.
const bufferSize = 32 * 1024

// Fields is a set of the header fields Scan keeps.
type Fields uint8

// The header fields Scan keeps, in fingerprint order.
const (
	From Fields = 1 << iota
	Date
	Subject
	To
	Cc
)

// fieldNames are the lowercase names of the kept fields, indexed by bit.
var fieldNames = [...]string{"from", "date", "subject", "to", "cc"}

// Has reports whether f holds every field in g.
func (f Fields) Has(g Fields) bool {
	return f&g == g
}

// All yields each field in f, in fingerprint order.
func (f Fields) All() iter.Seq[Fields] {
	return func(yield func(Fields) bool) {
		for i := range fieldNames {
			if g := fieldBit(i); f.Has(g) && !yield(g) {
				return
			}
		}
	}
}

// String returns the lowercase names of the fields in f, joined by commas,
// such as "from" or "to,cc".
func (f Fields) String() string {
	names := make([]string, 0, len(fieldNames))
	for g := range f.All() {
		names = append(names, fieldNames[bits.TrailingZeros8(uint8(g))])
	}

	return strings.Join(names, ",")
}

// Scanned is what Scan keeps of one Mail file.
type Scanned struct {
	// From, Date, Subject, To and Cc are the first occurrence of each
	// field, unfolded, without the whitespace after the colon, and at most
	// FieldCap bytes each. A missing field is nil.
	From, Date, Subject, To, Cc []byte

	// Truncated holds the fields cut at FieldCap.
	Truncated Fields

	// Body is the SHA-256 of the normalized body.
	Body [sha256.Size]byte
}

// Scanner reads Mail files one at a time, reusing its buffer and hash. The
// zero value is ready to use. A Scanner isn't safe for concurrent use.
type Scanner struct {
	br     *bufio.Reader
	hash   hash.Hash
	header header
	body   normalizer

	// sum is reused for the digest, which would escape to the heap if
	// summed into Scanned.
	sum []byte
}

// Scan reads one Mail file from r. It keeps the five fingerprint fields of
// the header, and streams the body through the normalizer into SHA-256.
// The returned slices are the caller's. Malformed content never fails: the
// error is only ever r's.
func (s *Scanner) Scan(r io.Reader) (Scanned, error) {
	if s.br == nil {
		s.br = bufio.NewReaderSize(r, bufferSize)
		s.hash = sha256.New()
	}

	s.br.Reset(r)
	s.hash.Reset()
	s.header = header{cur: -1, start: true}
	s.body = normalizer{hash: s.hash}

	err := s.readHeader()
	for err == nil {
		var frag []byte

		frag, err = s.br.ReadSlice('\n')
		s.body.write(frag)
		err = readError(err)
	}

	if !errors.Is(err, io.EOF) {
		return Scanned{}, err
	}

	h := &s.header
	out := Scanned{
		From:      h.values[0],
		Date:      h.values[1],
		Subject:   h.values[2],
		To:        h.values[3],
		Cc:        h.values[4],
		Truncated: h.truncated,
	}
	s.sum = s.hash.Sum(s.sum[:0])
	copy(out.Body[:], s.sum)

	return out, nil
}

// readHeader returns nil at the empty line that ends the header, and io.EOF
// when there is none.
func (s *Scanner) readHeader() error {
	for {
		frag, err := s.br.ReadSlice('\n')
		if s.header.line(frag) {
			return nil
		}

		if err = readError(err); err != nil {
			return err
		}
	}
}

// readError drops bufio.ErrBufferFull, which only means the line goes on in
// the next fragment.
func readError(err error) error {
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil
	}

	return err
}

func fieldBit(i int) Fields {
	return 1 << i
}
