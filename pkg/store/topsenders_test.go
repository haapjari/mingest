package store_test

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/ingest"
	"mingest/pkg/message"
	"mingest/pkg/store"
)

func TestDB_TopSenders_BreaksTiesByAddress(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)

	// Eleven Senders; the 10th and 11th tie at one Message.
	commit(t, db, 100, messagesFrom(map[string]int{
		"a@enron.com": 6, "b@enron.com": 5, "c@enron.com": 5,
		"d@enron.com": 4, "e@enron.com": 3, "f@enron.com": 3,
		"g@enron.com": 2, "h@enron.com": 2, "i@enron.com": 2,
		"k@enron.com": 1, "j@enron.com": 1,
	})...)

	assert.Equal(t, []ingest.TopSender{
		sent("a@enron.com", 6), sent("b@enron.com", 5), sent("c@enron.com", 5),
		sent("d@enron.com", 4), sent("e@enron.com", 3), sent("f@enron.com", 3),
		sent("g@enron.com", 2), sent("h@enron.com", 2), sent("i@enron.com", 2),
		sent("j@enron.com", 1),
	}, topSenders(t, db).Senders)
}

func TestDB_TopSenders_LeavesOutMessagesWithoutSender(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)
	commit(
		t,
		db,
		100,
		mailFile(1, ""),
		mailFile(2, ""),
		mailFile(3, "a@enron.com"),
	)

	assert.Equal(t, []ingest.TopSender{sent("a@enron.com", 1)},
		topSenders(t, db).Senders)
}

func TestDB_TopSenders_KeepsListAcrossWrites(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)
	commit(t, db, 100, mailFile(1, "a@enron.com"))
	want := []ingest.TopSender{sent("a@enron.com", 1)}

	finish(t, db, ingest.Finish{
		State: ingest.StateFailed,
		Error: ingest.Failure{Code: ingest.CodeInternal, Message: "x"},
	})
	assert.Equal(t, want, topSenders(t, db).Senders, "after Finish(failed)")

	beginWith(t, db, "0190c4a4-8d4e-7b2a-9f3e-1a2b3c4d5e70")
	assert.Empty(t, topSenders(t, db).Senders, "after Begin")

	opened(t, db)
	commit(t, db, 100, mailFile(1, "a@enron.com"))
	opened(t, db)
	assert.Equal(t, want, topSenders(t, db).Senders, "after Opened")

	finish(t, db, ingest.Finish{State: ingest.StateDone})
	assert.Equal(t, want, topSenders(t, db).Senders, "after Finish(done)")
}

func TestDB_TopSenders_ComesFromSameCommitAsStatus(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)
	st := commit(t, db, 100, mailFile(1, "a@enron.com"))

	assert.Equal(t, st, topSenders(t, db).Status)
}

func TestDB_TopSenders_SurvivesReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := beginIn(t, dir)
	opened(t, db)
	commit(t, db, 100, messagesFrom(map[string]int{
		"a@enron.com": 2, "b@enron.com": 1,
	})...)
	before := topSenders(t, db)

	require.NoError(t, db.Close())

	assert.Equal(t, before, topSenders(t, openStore(t, dir)))
}

func TestDB_TopSenders_ReturnsCopy(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)
	commit(t, db, 100, mailFile(1, "a@enron.com"))

	got := topSenders(t, db)
	got.Senders[0].Sender = "changed"

	assert.Equal(t, "a@enron.com", topSenders(t, db).Senders[0].Sender)
}

func TestDB_Commit_ChangesNothingWhenTopSendersUnreadable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := beginIn(t, dir)

	// SQLite sorts text after numbers, so this row comes first and can't
	// be scanned as a count.
	exec(t, sideConnection(t, dir),
		`INSERT INTO sender VALUES ('z@enron.com', 'many')`)
	before := opened(t, db)

	_, err := db.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.Error(t, err)
	assert.Equal(t, before, status(t, db))
}

func TestDB_Opened_ChangesNothingWhenItFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := beginIn(t, dir)
	exec(
		t,
		sideConnection(t, dir),
		`CREATE TRIGGER fail BEFORE UPDATE ON ingestion
		BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	)

	before := status(t, db)

	_, err := db.Opened(context.Background(), ingest.Opened{Size: 1, At: t0})
	require.Error(t, err)
	assert.Equal(t, before, status(t, db))

	top, err := db.TopSenders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before, top.Status)
}

func TestDB_TopSenders_ShowsReadersOnlyWholeCommits(t *testing.T) {
	t.Parallel()

	db := begun(t)
	opened(t, db)

	// want maps each revision to the top ten a Go map counts after it.
	want := map[int64][]ingest.TopSender{}
	initial := topSenders(t, db)
	want[initial.Status.Revision] = initial.Senders

	stop := make(chan struct{})
	seen := readConcurrently(t, db, stop)

	counts := map[string]int64{}
	for k := range uint64(50) {
		files := commitBatch(k)
		st := commit(t, db, int64(k+1)*100, files...)

		for _, f := range files {
			counts[f.Sender]++
		}

		want[st.Revision] = ranked(counts)
	}

	close(stop)

	observed := seen()
	require.NotEmpty(t, observed)

	for _, got := range observed {
		assert.Equal(t, want[got.Status.Revision], got.Senders,
			"revision %d", got.Status.Revision)
	}
}

func sent(sender string, messages int64) ingest.TopSender {
	return ingest.TopSender{Sender: sender, Messages: messages}
}

func topSenders(t *testing.T, db *store.DB) ingest.TopSenders {
	t.Helper()

	top, err := db.TopSenders(context.Background())
	require.NoError(t, err)

	return top
}

func finish(t *testing.T, db *store.DB, f ingest.Finish) ingest.Status {
	t.Helper()

	f.At = t0

	return write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Finish(ctx, f)
	})
}

// messagesFrom makes each Sender's number of Messages, all different.
func messagesFrom(senders map[string]int) []message.Identity {
	var (
		out []message.Identity
		n   uint64
	)

	for _, sender := range slices.Sorted(maps.Keys(senders)) {
		for range senders[sender] {
			n++
			out = append(out, mailFile(n, sender))
		}
	}

	return out
}

// commitBatch is chunk k of the concurrent test: 20 new Messages over 30
// Senders, so the ranking keeps changing.
func commitBatch(k uint64) []message.Identity {
	out := make([]message.Identity, 0, 20)
	for i := range uint64(20) {
		n := k*20 + i + 1
		out = append(out, mailFile(n, fmt.Sprintf("s%02d@enron.com", n*n%30)))
	}

	return out
}

// ranked is the Top Senders of counts: most Messages first, ties by address.
func ranked(counts map[string]int64) []ingest.TopSender {
	out := make([]ingest.TopSender, 0, len(counts))
	for sender, n := range counts {
		out = append(out, ingest.TopSender{Sender: sender, Messages: n})
	}

	slices.SortFunc(out, func(a, b ingest.TopSender) int {
		return cmp.Or(cmp.Compare(b.Messages, a.Messages),
			strings.Compare(a.Sender, b.Sender))
	})

	return out[:min(len(out), ingest.TopSendersLimit)]
}

// readConcurrently reads Status and TopSenders in four goroutines until
// stop closes, checking that the revision never goes backwards. The
// returned function waits for them and returns every list they saw.
func readConcurrently(
	t *testing.T,
	db *store.DB,
	stop <-chan struct{},
) func() []ingest.TopSenders {
	t.Helper()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen []ingest.TopSenders
	)

	for range 4 {
		wg.Go(func() {
			got := readUntil(t, db, stop)

			mu.Lock()
			seen = append(seen, got...)
			mu.Unlock()
		})
	}

	return func() []ingest.TopSenders {
		wg.Wait()
		return seen
	}
}

func readUntil(
	t *testing.T,
	db *store.DB,
	stop <-chan struct{},
) []ingest.TopSenders {
	var (
		out  []ingest.TopSenders
		last int64
	)

	for {
		select {
		case <-stop:
			return out
		default:
		}

		st, err := db.Status(context.Background())
		assert.NoError(t, err)

		top, err := db.TopSenders(context.Background())
		assert.NoError(t, err)

		assert.GreaterOrEqual(t, top.Status.Revision, st.Revision)
		assert.GreaterOrEqual(t, st.Revision, last)
		last = top.Status.Revision
		out = append(out, top)
	}
}
