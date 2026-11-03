package dataset

import (
	"bytes"
	"context"
	_ "embed"
	"io"

	"golang.org/x/tools/txtar"

	"mingest/pkg/source"
)

// The fixture's expected values, source: testdata/fixture.txtar
const (
	FixtureMailFiles     = 22
	FixtureMessages      = 19
	FixtureSenders       = 14
	FixtureWithoutSender = 1
)

// FixtureTopSenders is the fixture's Top Senders, counted by hand. The 10th
// place ties with sally.beck@enron.com and wins by address.
var FixtureTopSenders = []TopSender{
	{"eric.bass@enron.com", 3},
	{"phillip.allen@enron.com", 2},
	{"steven.kean@enron.com", 2},
	{"greg.whalley@enron.com", 1},
	{"jeff.skilling@enron.com", 1},
	{"john..doe@enron.com", 1},
	{"kenneth.lay@enron.com", 1},
	{"louise.kitchen@enron.com", 1},
	{"mark.taylor@enron.com", 1},
	{"richard.shapiro@enron.com", 1},
}

//go:embed testdata/fixture.txtar
var fixtureTxtar []byte

// TopSender is one entry of Top Senders.
type TopSender struct {
	Sender   string
	Messages int
}

// Memory is a Dataset held in memory. It's a source.Source whose Size and
// Version the test sets, and an io.WriterTo for WriteFile.
type Memory struct {
	Data    []byte
	Size    int64
	Version string
}

// Fixture returns the hand-written fixture as a tar.gz in memory, with Size
// set to its length and Version to "fixture".
func Fixture() *Memory {
	var buf bytes.Buffer

	a := newArchive(&buf)
	for _, f := range txtar.Parse(fixtureTxtar).Files {
		a.add(f.Name, f.Data)
	}

	// Writing to a bytes.Buffer never returns an error, and the embedded
	// fixture is fixed at build time, so close cannot fail here. Any mistake
	// in the fixture surfaces as a failing test, not a returned error.
	_ = a.close()

	return &Memory{
		Data:    buf.Bytes(),
		Size:    int64(buf.Len()),
		Version: "fixture",
	}
}

// Open returns a fresh reader over Data on every call.
func (m *Memory) Open(_ context.Context) (*source.Stream, error) {
	return &source.Stream{
		ReadCloser: io.NopCloser(bytes.NewReader(m.Data)),
		URL:        "memory:fixture",
		Size:       m.Size,
		Version:    m.Version,
	}, nil
}

// WriteTo writes Data to w.
func (m *Memory) WriteTo(w io.Writer) (int64, error) {
	return bytes.NewReader(m.Data).WriteTo(w)
}
