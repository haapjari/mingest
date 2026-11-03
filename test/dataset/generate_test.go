package dataset_test

import (
	"bytes"
	"cmp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/dataset"
)

func TestGenerate_IsDeterministicFromSeed(t *testing.T) {
	first := generate(t, params(1, 100))

	assert.Equal(
		t,
		first,
		generate(t, params(1, 100)),
		"the same seed and size yield a byte-identical stream",
	)

	assert.NotEqual(
		t,
		first,
		generate(t, params(2, 100)),
		"a different seed yields a different stream",
	)
}

func TestSynthetic_Plan_MatchesStream(t *testing.T) {
	p := params(7, 10000)
	plan := dataset.Generate(p).Plan()

	mails := decodeMails(t, p)
	messages := distinct(mails)
	perSender := messagesPerSender(messages)

	assert.Equal(t, 10000, plan.MailFiles)
	assert.Len(t, mails, plan.MailFiles)
	assert.Equal(t, 4926, plan.Messages, "10000 Mail files / 2.03, rounded")
	assert.Len(t, messages, plan.Messages, "Copies share Subject and Sender")
	assert.Len(t, perSender, plan.Senders)
	assert.Equal(t, topSenders(perSender), plan.TopSenders)
}

func TestSynthetic_Plan_ListsOnlySendersWithMessages(t *testing.T) {
	plan := dataset.Generate(params(7, 2)).Plan()

	assert.Equal(t, 1, plan.Messages)
	assert.Equal(t, 1, plan.Senders)
	assert.Len(t, plan.TopSenders, 1)
}

func TestSynthetic_WriteTo_KeepsHeapFlat(t *testing.T) {
	small := peakHeap(t, 10_000)
	large := peakHeap(t, flatMailFiles)

	assert.Less(
		t,
		large,
		2*small,
		"peak heap at %d Mail files vs 10k",
		flatMailFiles,
	)
}

func TestGenerate_VariesCopiesOnlyOutsideIdentity(t *testing.T) {
	files := readArchive(t, bytes.NewReader(generate(t, params(5, 1000))))

	ids, dates := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		text := string(f.data)
		folder := strings.Split(f.name, "/")[1]

		assert.Equal(t, "X-Folder: "+folder, lineWithPrefix(text, "X-Folder: "))
		ids[lineWithPrefix(text, "Message-ID: ")] = true
		dates[lineWithPrefix(text, "Date: ")] = true
	}

	assert.Len(t, ids, len(files), "every Mail file has its own Message-ID")
	assert.Len(t, dates, dataset.Generate(params(5, 1000)).Plan().Messages,
		"every Message has its own Date")
}

func params(seed uint64, mailFiles int) dataset.Params {
	return dataset.Params{
		Seed:      seed,
		MailFiles: mailFiles,
		CopyRatio: 2.03,
		Senders:   50,
		BodyMin:   10,
		BodyMax:   200,
	}
}

func generate(t *testing.T, p dataset.Params) []byte {
	t.Helper()

	var buf bytes.Buffer

	n, err := dataset.Generate(p).WriteTo(&buf)
	require.NoError(t, err)
	require.Equal(t, int64(buf.Len()), n)

	return buf.Bytes()
}

// mailHeader is what a Mail file says about its Message.
type mailHeader struct {
	subject, from string
}

// decodeMails reads the Dataset p generates and returns each Mail file's
// Subject and From.
func decodeMails(t *testing.T, p dataset.Params) []mailHeader {
	t.Helper()

	var mails []mailHeader

	for _, f := range readArchive(t, bytes.NewReader(generate(t, p))) {
		text := string(f.data)
		mails = append(mails, mailHeader{
			subject: lineWithPrefix(text, "Subject: "),
			from: strings.TrimPrefix(
				lineWithPrefix(text, "From: "),
				"From: ",
			),
		})
	}

	return mails
}

// distinct is the set of Messages: Copies of one collapse into one entry.
func distinct(mails []mailHeader) map[mailHeader]bool {
	set := map[mailHeader]bool{}
	for _, m := range mails {
		set[m] = true
	}

	return set
}

func messagesPerSender(messages map[mailHeader]bool) map[string]int {
	counts := map[string]int{}
	for m := range messages {
		counts[m.from]++
	}

	return counts
}

// topSenders orders by Messages descending, then by address.
func topSenders(messages map[string]int) []dataset.TopSender {
	var top []dataset.TopSender
	for sender, n := range messages {
		top = append(top, dataset.TopSender{Sender: sender, Messages: n})
	}

	slices.SortFunc(top, func(a, b dataset.TopSender) int {
		return cmp.Or(
			cmp.Compare(b.Messages, a.Messages),
			strings.Compare(a.Sender, b.Sender),
		)
	})

	return top[:min(10, len(top))]
}

// peakHeap streams a Dataset to nowhere and returns the largest HeapInuse
// seen while writing.
func peakHeap(t *testing.T, mailFiles int) uint64 {
	t.Helper()

	p := params(3, mailFiles)
	p.BodyMin, p.BodyMax = 0, 20

	runtime.GC()

	w := &heapWriter{}
	_, err := dataset.Generate(p).WriteTo(w)
	require.NoError(t, err)

	return w.peak
}

// heapWriter discards what it gets and samples HeapInuse on the first write
// and every 1024th after it.
type heapWriter struct {
	writes int
	peak   uint64
}

func (h *heapWriter) Write(p []byte) (int, error) {
	h.writes++
	if h.writes%1024 == 1 {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		h.peak = max(h.peak, m.HeapInuse)
	}

	return len(p), nil
}
