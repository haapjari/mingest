package ingest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"mingest/pkg/message"
	"mingest/pkg/source"
	"mingest/test/dataset"
)

func TestIngester_Run_ResumeFailsOnChangedSource(t *testing.T) {
	t.Parallel()

	changes := map[string]func(*source.Stream){
		"size":          func(s *source.Stream) { s.Size++ },
		"version":       func(s *source.Stream) { s.Version = "other" },
		"url":           func(s *source.Stream) { s.URL = "memory:other" },
		"empty version": func(s *source.Stream) { s.Version = "" },
	}

	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				st := resumeOn(t, changedSource(change))

				assert.Equal(t, StateFailed, st.State)
				assert.Equal(t, CodeSourceChanged, st.Error.Code)
			})
		})
	}
}

func TestIngester_Run_RecoversFromSourceFailingAtAnyByte(t *testing.T) {
	t.Parallel()

	want := fixtureIdentities(t)

	for n := range int64(len(fixture())) {
		synctest.Test(t, func(t *testing.T) {
			store := newFakeStore()
			ing := newTestIngester(t, store, failOnce(n+1))

			st := ingestToEnd(t, ing)

			require.Equal(t, StateDone, st.State, "failed after %d", n+1)
			assertFixtureCounts(t, st)
			require.Equal(t, want, committedIdentities(store),
				"failed after %d", n+1)
		})
	}
}

func TestIngester_Run_GivesUpWhenSourceFailsEveryRetry(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		src := &scriptedSource{}
		src.open = func(int) (*source.Stream, error) {
			return nil, errors.New("connection refused")
		}

		ing := newTestIngester(t, newFakeStore(), src)

		st := ingestToEnd(t, ing)

		assert.Equal(t, StateFailed, st.State)
		assert.Equal(t, CodeSourceUnreachable, st.Error.Code)
		require.Len(t, src.opened, 6)

		for k := 1; k < len(src.opened); k++ {
			wait := src.opened[k].Sub(src.opened[k-1])
			assert.LessOrEqual(t, wait, time.Second<<(k-1), "wait %d", k)
		}

		// Jittered, not skipped: five random waits are never all zero.
		assert.Positive(t, src.opened[len(src.opened)-1].Sub(src.opened[0]))
	})
}

func TestIngester_Run_ResetsRetryCountAfterCommit(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// Three failures, an attempt that commits and then fails, four
		// more failures: never more than five in a row.
		data := fixture()
		src := &scriptedSource{}
		src.open = func(n int) (*source.Stream, error) {
			switch n {
			case 3:
				return failingStream(data, int64(len(data)-1)), nil
			case 8:
				return fixtureStream(), nil
			default:
				return nil, errors.New("connection refused")
			}
		}

		st := ingestToEnd(t, newTestIngester(t, newFakeStore(), src))

		assert.Equal(t, StateDone, st.State)
		assertFixtureCounts(t, st)
	})
}

func TestIngester_Run_FailsWithCodeForEachError(t *testing.T) {
	t.Parallel()

	data := fixture()
	tests := map[string]struct {
		src      source.Source
		onCommit func(context.Context, Chunk) error
		err      error
		want     Code
	}{
		"disk full": {
			src:      fixtureSource(),
			onCommit: failFirst(&sync.Once{}, ErrDiskFull),
			err:      ErrDiskFull,
			want:     CodeDiskFull,
		},
		"corrupt archive": {
			src:  &dataset.Memory{Data: data[:len(data)/2], Version: "v"},
			err:  errArchiveCorrupt,
			want: CodeArchiveCorrupt,
		},
		"unexpected error": {
			src:      fixtureSource(),
			onCommit: failFirst(&sync.Once{}, errors.New("/data/x.db: boom")),
			err:      errors.New("other"),
			want:     CodeInternal,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				store := newFakeStore()
				store.onCommit = tc.onCommit

				st := ingestToEnd(t, newTestIngester(t, store, tc.src))

				assert.Equal(t, StateFailed, st.State)
				assert.Equal(t, tc.want, st.Error.Code)
				assert.Equal(t, failure(tc.err).Message, st.Error.Message)
			})
		})
	}
}

func TestFailure_HasFixedMessageForEveryCode(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		ErrDiskFull, errSourceChanged,
		errSourceUnreachable, errArchiveCorrupt,
	} {
		assert.NotEmpty(t, failure(err).Message, err)
	}

	got := failure(errors.New("/data/0190.db: disk I/O error"))
	assert.Equal(t, CodeInternal, got.Code)
	assert.NotContains(t, got.Message, "/data")
}

func TestIngester_Run_WaitsWhileStoreStatusFails(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		_, err := store.Begin(
			t.Context(),
			Begin{ID: "a", SourceURL: fixtureURL},
		)
		require.NoError(t, err)
		store.failStatus(1)

		src := &scriptedSource{open: func(int) (*source.Stream, error) {
			return fixtureStream(), nil
		}}
		stop := runIngester(t, newTestIngester(t, store, src))
		defer stop()

		synctest.Wait()
		assert.Empty(t, src.opened)
	})
}

func TestIngester_Run_FailsOnStoreStatusErrorAtOpen(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		src := &scriptedSource{open: func(int) (*source.Stream, error) {
			store.failStatus(1)
			return fixtureStream(), nil
		}}

		st := ingestToEnd(t, newTestIngester(t, store, src))

		assert.Equal(t, StateFailed, st.State)
		assert.Equal(t, CodeInternal, st.Error.Code)
	})
}

func TestIngester_Run_StopsRetryingWhenCancelledDuringBackoff(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		src := &scriptedSource{open: func(int) (*source.Stream, error) {
			return nil, errors.New("connection refused")
		}}
		ing := newTestIngester(t, newFakeStore(), src)
		stop := runIngester(t, ing)

		_, err := ing.Start(t.Context())
		require.NoError(t, err)

		synctest.Wait()
		stop()

		assert.Len(t, src.opened, 1)
		assert.InDelta(t, 1, value(t, ing.metrics.sourceRetries), 0)
	})
}

func TestIngester_Run_DoesNotRetryAfterShutdown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ing := newTestIngester(t, newFakeStore(), hangingSource{})
		stop := runIngester(t, ing)

		_, err := ing.Start(t.Context())
		require.NoError(t, err)

		synctest.Wait()
		stop()

		assert.InDelta(t, 0, value(t, ing.metrics.sourceRetries), 0)
	})
}

func TestIngester_Run_UsesUpWaitingStart(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		store.onFinish = func(context.Context, Finish) error {
			return errors.New("disk gone")
		}
		_, err := store.Begin(
			t.Context(),
			Begin{ID: "a", SourceURL: fixtureURL},
		)
		require.NoError(t, err)

		ing := newTestIngester(t, store, fixtureSource())
		ing.starts <- struct{}{}
		ing.ingest(t.Context())

		assert.Empty(t, ing.starts, "a second run of the same Ingestion")
	})
}

func TestIngester_Run_FailsOnDiskFullAtOpen(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		store.onOpened = func(context.Context, Opened) error {
			return ErrDiskFull
		}

		st := ingestToEnd(t, newTestIngester(t, store, fixtureSource()))

		assert.Equal(t, StateFailed, st.State)
		assert.Equal(t, CodeDiskFull, st.Error.Code)
	})
}

func TestIngester_Run_LeavesRunningWhenFinishFails(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		store.onFinish = func(context.Context, Finish) error {
			return errors.New("disk gone")
		}

		ing := newTestIngester(t, store, fixtureSource())
		stop := runIngester(t, ing)
		defer stop()

		_, err := ing.Start(t.Context())
		require.NoError(t, err)
		synctest.Wait()

		st, err := ing.Status(t.Context())
		require.NoError(t, err)
		assert.Equal(t, StateRunning, st.State)
	})
}

// Not parallel: goleak sees every goroutine in the process.
func TestIngester_Run_LeavesRunningWhenCancelled(t *testing.T) {
	running := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, running) })

	synctest.Test(t, func(t *testing.T) {
		st := interrupt(t, newFakeStore(), fixtureSource())

		assert.Equal(t, StateRunning, st.State)
	})
}

// testOptions has small chunks, so the fixture makes several, and the
// real retry numbers.
func testOptions() Options {
	return Options{
		ChunkMailFiles:  3,
		ChunkBytes:      4 * 1024 * 1024,
		ChunkAge:        time.Hour,
		ChannelCapacity: 2,
		ReadBuffer:      1024,
		SourceRetries:   5,
		RetryBase:       time.Second,
		RetryMax:        30 * time.Second,
		Logger:          slog.New(slog.DiscardHandler),
	}
}

func newTestIngester(t *testing.T, store Store, src source.Source) *Ingester {
	t.Helper()

	o := testOptions()
	o.SourceURL = fixtureURL

	ing, err := New(store, src, o)
	require.NoError(t, err)

	return ing
}

// fixtureURL is the URL dataset.Memory streams report.
const fixtureURL = "memory:fixture"

// runIngester runs the Ingester until the returned stop is called, which
// waits for Run to return nil.
func runIngester(t *testing.T, ing *Ingester) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)

	go func() { errs <- ing.Run(ctx) }()

	return func() {
		cancel()
		assert.NoError(t, <-errs)
	}
}

// ingestToEnd starts an Ingestion on the Ingester and returns its final
// Status.
func ingestToEnd(t *testing.T, ing *Ingester) Status {
	t.Helper()

	stop := runIngester(t, ing)
	defer stop()

	_, err := ing.Start(t.Context())
	require.NoError(t, err)

	return waitForEnd(t, ing)
}

// waitForEnd waits until the Ingestion isn't running, through Changed.
func waitForEnd(t *testing.T, ing *Ingester) Status {
	t.Helper()

	for {
		changed := ing.Changed()

		st, err := ing.Status(t.Context())
		require.NoError(t, err)

		if st.State != StateRunning {
			return st
		}

		<-changed
	}
}

// interrupt starts an Ingestion on store and stops Run after its first
// commit, as a shutdown would. It returns the Status left behind.
func interrupt(t *testing.T, store *fakeStore, src source.Source) Status {
	t.Helper()

	ing := newTestIngester(t, store, src)
	stop := runIngester(t, ing)

	committed := make(chan struct{})
	once := sync.Once{}
	store.afterCommit = func() { once.Do(func() { close(committed) }) }
	store.onCommit = func(ctx context.Context, _ Chunk) error {
		select {
		case <-committed:
			<-ctx.Done()
			return ctx.Err()
		default:
			return nil
		}
	}

	_, err := ing.Start(t.Context())
	require.NoError(t, err)

	<-committed
	synctest.Wait()
	stop()

	store.onCommit, store.afterCommit = nil, nil

	st, err := store.Status(t.Context())
	require.NoError(t, err)

	return st
}

// resumeOn interrupts an Ingestion of the fixture, then resumes it on the
// given Source.
func resumeOn(t *testing.T, src source.Source) Status {
	t.Helper()

	store := newFakeStore()
	interrupt(t, store, fixtureSource())

	ing := newTestIngester(t, store, src)
	stop := runIngester(t, ing)
	defer stop()

	return waitForEnd(t, ing)
}

func assertFixtureCounts(t *testing.T, st Status) {
	t.Helper()

	assert.Equal(t, int64(dataset.FixtureMailFiles), st.MailFiles)
	assert.Equal(t, int64(dataset.FixtureMessages), st.Messages)
	assert.Equal(t, int64(dataset.FixtureSenders), st.Senders)
	assert.Equal(t, int64(len(fixture())), st.BytesRead)
}

func fixtureIdentities(t *testing.T) []message.Identity {
	t.Helper()

	return identities(archiveFiles(t, fixture()))
}

// failFirst fails the first commit it sees with err.
func failFirst(once *sync.Once, err error) func(context.Context, Chunk) error {
	return func(context.Context, Chunk) error {
		var out error
		once.Do(func() { out = err })

		return out
	}
}

func fixtureSource() source.Source {
	return dataset.Fixture()
}

func fixtureStream() *source.Stream {
	// dataset.Memory.Open never fails.
	s, _ := dataset.Fixture().Open(context.Background())
	return s
}

// failingStream serves the first n bytes of data, then fails.
func failingStream(data []byte, n int64) *source.Stream {
	s := fixtureStream()
	s.ReadCloser = io.NopCloser(&failingReader{
		r:   io.LimitReader(bytes.NewReader(data), n),
		err: io.ErrUnexpectedEOF,
	})

	return s
}

// failOnce is the fixture Source whose first Stream fails after n bytes.
func failOnce(n int64) source.Source {
	data := fixture()
	src := &scriptedSource{}
	src.open = func(k int) (*source.Stream, error) {
		if k == 0 {
			return failingStream(data, n), nil
		}

		return fixtureStream(), nil
	}

	return src
}

// changedSource is the fixture Source with every Stream changed by change.
func changedSource(change func(*source.Stream)) source.Source {
	src := &scriptedSource{}
	src.open = func(int) (*source.Stream, error) {
		s := fixtureStream()
		change(s)

		return s, nil
	}

	return src
}

// hangingSource streams nothing until the ctx of its Open ends, and then
// fails, as an HTTP body does when its request is cancelled.
type hangingSource struct{}

func (hangingSource) Open(ctx context.Context) (*source.Stream, error) {
	s := fixtureStream()
	s.ReadCloser = io.NopCloser(ctxReader{ctx})

	return s, nil
}

type ctxReader struct{ ctx context.Context }

func (r ctxReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

// scriptedSource answers its k-th Open, from 0, with open(k), and records
// when each Open came.
type scriptedSource struct {
	mu     sync.Mutex
	opened []time.Time
	open   func(k int) (*source.Stream, error)
}

func (s *scriptedSource) Open(context.Context) (*source.Stream, error) {
	s.mu.Lock()
	k := len(s.opened)
	s.opened = append(s.opened, time.Now())
	s.mu.Unlock()

	return s.open(k)
}
