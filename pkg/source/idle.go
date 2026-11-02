package source

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// errIdle is a read that failed because no byte came for the idle timeout.
var errIdle = errors.New("source: no bytes received")

// idleReader cancels a read that stalls. A timer runs only while a Read
// waits, and calls cancel when one has waited timeout for data. The time
// between reads, when the caller is busy, doesn't count. The read that
// cancel makes fail is reported as errIdle.
type idleReader struct {
	r       io.Reader
	timeout time.Duration
	timer   *time.Timer
	fired   atomic.Bool
}

func newIdleReader(
	r io.Reader,
	timeout time.Duration,
	cancel func(),
) *idleReader {
	i := &idleReader{r: r, timeout: timeout}
	i.timer = time.AfterFunc(timeout, func() {
		i.fired.Store(true)
		cancel()
	})
	i.timer.Stop()

	return i
}

func (i *idleReader) Read(p []byte) (int, error) {
	i.timer.Reset(i.timeout)
	n, err := i.r.Read(p)
	i.timer.Stop()

	return n, i.wrap(err)
}

// Stop stops the timer, so cancel is never called.
func (i *idleReader) Stop() {
	i.timer.Stop()
}

// wrap marks a read error that came after the timer fired, io.EOF
// included: a cancelled body can end with io.EOF, which must not pass for a
// complete stream.
func (i *idleReader) wrap(err error) error {
	if err == nil || !i.fired.Load() {
		return err
	}

	return fmt.Errorf("%w for %v: %w", errIdle, i.timeout, err)
}
