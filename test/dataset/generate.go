package dataset

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// Parameters of the Zipf distribution that picks a Sender for each Message.
// Real mailboxes are lopsided: a few Senders write most of the mail and most
// Senders write very little. Zipf's law models this: the busiest Sender sends
// the most, the second busiest about half as much, and so on down a long tail.
// zipfS controls how lopsided it is (higher means the top Senders dominate
// more). zipfV softens the very top so the busiest Sender does not overwhelm
// the rest; it must be at least 1.
const (
	zipfS = 1.2
	zipfV = 1
)

const lorem = "Please review the attached forecast and send your comments " +
	"before the meeting on Friday. The numbers for the west desk are final. "

var start = time.Date(2001, time.May, 14, 9, 0, 0, 0, time.UTC)

// Params shape a synthetic Dataset.
type Params struct {
	// Seed makes the Dataset: the same Params give the same bytes.
	Seed uint64

	// MailFiles is the number of Mail files, at least 1.
	MailFiles int

	// CopyRatio is Mail files per Message, at least 1. Enron's is about 2.03.
	CopyRatio float64

	// Senders is the number of addresses the Senders are drawn from, between
	// 1 and 100000, on a skewed (Zipf) distribution.
	Senders int

	// BodyMin and BodyMax bound each body's length in bytes.
	BodyMin, BodyMax int
}

// Plan is what a synthetic Dataset holds, known from its Params without
// reading what it wrote.
type Plan struct {
	MailFiles, Messages, Senders int
	TopSenders                   []TopSender
}

// Synthetic is a generated Dataset. It's rebuilt from its Params on every
// WriteTo and holds none of its Mail files.
type Synthetic struct {
	p Params

	messages int

	// Message i has base Copies, plus one more when i < extra.
	base, extra int
}

// Generate returns the synthetic Dataset that p describes.
func Generate(p Params) *Synthetic {
	messages := max(1, int(math.Round(float64(p.MailFiles)/p.CopyRatio)))

	return &Synthetic{
		p:        p,
		messages: messages,
		base:     p.MailFiles / messages,
		extra:    p.MailFiles % messages,
	}
}

// Plan draws every Message's Sender once and keeps one count per Sender.
func (s *Synthetic) Plan() Plan {
	counts := make([]int, s.p.Senders)
	for i := range s.messages {
		counts[s.sender(s.rng(i))]++
	}

	return Plan{
		MailFiles:  s.p.MailFiles,
		Messages:   s.messages,
		Senders:    senders(counts),
		TopSenders: top(counts),
	}
}

// WriteTo streams the Dataset to w as a tar.gz. Copies are written in rounds,
// round r holding Copy r of every Message that has one, so the Copies of a
// Message are spread across the archive.
func (s *Synthetic) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	a := newArchive(cw)

	s.writeRounds(a)
	err := a.close()

	return cw.n, err
}

func (s *Synthetic) writeRounds(a *archive) {
	var buf bytes.Buffer

	for round := range s.base + 1 {
		for i := range s.inRound(round) {
			buf.Reset()
			s.render(&buf, i, round)

			a.add(
				fmt.Sprintf("mailbox%03d/folder%d/%d.", i%1000, round, i),
				buf.Bytes(),
			)
		}
	}
}

// inRound is the number of Messages with a Copy in round: the first ones.
func (s *Synthetic) inRound(round int) int {
	if round < s.base {
		return s.messages
	}

	return s.extra
}

// render writes Copy round of Message i. Copies differ only in Message-ID
// and X-Folder, which aren't part of a Message's identity.
func (s *Synthetic) render(buf *bytes.Buffer, i, round int) {
	r := s.rng(i)
	from := address(s.sender(r))
	to := address(r.IntN(s.p.Senders))
	date := start.Add(time.Duration(i) * time.Minute).Format(time.RFC1123Z)

	fmt.Fprintf(buf, "Message-ID: <%d.%d.synthetic>\n", i, round)
	fmt.Fprintf(buf, "Date: %s\nFrom: %s\nTo: %s\n", date, from, to)
	fmt.Fprintf(buf, "Subject: synthetic message %d\n", i)
	fmt.Fprintf(buf, "X-Folder: folder%d\n\n", round)
	s.body(buf, r)
}

// body writes BodyMin to BodyMax bytes of text in lines of 72.
func (s *Synthetic) body(buf *bytes.Buffer, r *rand.Rand) {
	n := s.p.BodyMin + r.IntN(s.p.BodyMax-s.p.BodyMin+1)
	off := r.IntN(len(lorem))

	for k := range n {
		buf.WriteByte(bodyByte(off, k))
	}
}

// rng is Message i's own generator, so any Message is rebuilt alone.
//
//nolint:gosec // G404: seeded on purpose; G115: i is never negative
func (s *Synthetic) rng(i int) *rand.Rand {
	return rand.New(rand.NewPCG(s.p.Seed, uint64(i)))
}

// sender draws a Sender's index; it's the first draw from a Message's rng.
//
//nolint:gosec // G115: Senders is at least 1, and the draw is below it
func (s *Synthetic) sender(r *rand.Rand) int {
	imax := uint64(s.p.Senders - 1)
	return int(rand.NewZipf(r, zipfS, zipfV, imax).Uint64())
}

// senders counts the Senders that sent at least one Message.
func senders(counts []int) int {
	n := 0

	for _, c := range counts {
		if c > 0 {
			n++
		}
	}

	return n
}

// top orders the Senders by count, then by address, which zero-padding makes
// the order of their indexes.
func top(counts []int) []TopSender {
	order := make([]int, len(counts))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(counts[b], counts[a])
	})

	var out []TopSender
	for _, i := range order[:min(10, senders(counts))] {
		out = append(out, TopSender{Sender: address(i), Messages: counts[i]})
	}

	return out
}

func address(i int) string {
	return fmt.Sprintf("sender%05d@enron.com", i)
}

func bodyByte(off, k int) byte {
	if k%72 == 71 {
		return '\n'
	}

	return lorem[(off+k)%len(lorem)]
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)

	return n, err
}
