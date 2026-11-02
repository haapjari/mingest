package source_test

import (
	"context"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/source"
)

const idleTimeout = time.Minute

func TestIdleReader_Read_FailsAfterExactlyTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		in := newChanReader()
		idle := source.NewIdleReader(in, idleTimeout, in.cancel)
		defer idle.Stop()

		errs := readUntilError(idle)

		time.Sleep(idleTimeout - time.Nanosecond)
		synctest.Wait()
		assert.False(t, in.cancelled(), "cancelled before the timeout")

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		assert.True(t, in.cancelled(), "not cancelled at the timeout")

		err := <-errs
		require.ErrorIs(t, err, source.ErrIdle)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestIdleReader_Read_WaitsWhileBytesKeepComing(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		in := newChanReader()
		idle := source.NewIdleReader(in, idleTimeout, in.cancel)
		defer idle.Stop()

		errs := readUntilError(idle)

		for range 5 {
			time.Sleep(idleTimeout - time.Nanosecond)
			in.data <- []byte("x")
			synctest.Wait()
			assert.False(t, in.cancelled())
		}

		close(in.data)
		assert.ErrorIs(t, <-errs, io.EOF)
		assert.False(t, in.cancelled())
	})
}

// The timer runs only while a Read waits: a caller that is busy between
// reads, as Read is while Write applies backpressure, isn't a stall.
func TestIdleReader_Read_DoesNotCountTimeBetweenReads(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		in := newChanReader()
		idle := source.NewIdleReader(in, idleTimeout, in.cancel)
		defer idle.Stop()

		go func() { in.data <- []byte("a") }()
		_, err := idle.Read(make([]byte, 8))
		require.NoError(t, err)

		time.Sleep(2 * idleTimeout)
		synctest.Wait()
		assert.False(t, in.cancelled())
	})
}

// A read the timer cancelled can end with io.EOF, which can't be told from
// the end of the stream, so it is a stall too: the caller retries rather
// than trust a stream that may be cut short.
func TestIdleReader_Read_MarksEndAfterTimeoutAsIdle(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		done := make(chan struct{})
		idle := source.NewIdleReader(eofOnCancel(done), idleTimeout,
			func() { close(done) })
		defer idle.Stop()

		_, err := idle.Read(make([]byte, 8))
		require.ErrorIs(t, err, source.ErrIdle)
		assert.ErrorIs(t, err, io.EOF)
	})
}

// eofOnCancel blocks every Read until done closes, then reports io.EOF.
type eofOnCancel chan struct{}

func (e eofOnCancel) Read([]byte) (int, error) {
	<-e

	return 0, io.EOF
}

func TestIdleReader_Stop_DisarmsTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		in := newChanReader()
		idle := source.NewIdleReader(in, idleTimeout, in.cancel)
		idle.Stop()

		time.Sleep(2 * idleTimeout)
		synctest.Wait()
		assert.False(t, in.cancelled())
	})
}

// chanReader reads the byte slices sent on data, io.EOF once data is
// closed, and context.Canceled once cancel is called.
type chanReader struct {
	data chan []byte
	done chan struct{}
	once sync.Once
}

func newChanReader() *chanReader {
	return &chanReader{data: make(chan []byte), done: make(chan struct{})}
}

func (r *chanReader) Read(p []byte) (int, error) {
	select {
	case b, ok := <-r.data:
		if !ok {
			return 0, io.EOF
		}

		return copy(p, b), nil
	case <-r.done:
		return 0, context.Canceled
	}
}

func (r *chanReader) cancel() {
	r.once.Do(func() { close(r.done) })
}

func (r *chanReader) cancelled() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// readUntilError reads r in a goroutine and sends the first error.
func readUntilError(r io.Reader) <-chan error {
	errs := make(chan error, 1)

	go func() {
		buf := make([]byte, 8)
		for {
			if _, err := r.Read(buf); err != nil {
				errs <- err

				return
			}
		}
	}()

	return errs
}
