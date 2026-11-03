package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
	"uuid"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"mingest/pkg/source"
)

// finishAttempts is how many times finish tries to record the end.
const finishAttempts = 3

// ErrRunning is returned by Start when an Ingestion is already running.
var ErrRunning = errors.New("ingestion running")

var (
	// errSourceChanged means a resume found a different archive from the
	// one the Ingestion started on, or one it couldn't compare.
	errSourceChanged = errors.New("source changed")

	// errSourceUnreachable means the Source kept failing through every
	// retry.
	errSourceUnreachable = errors.New("source unreachable")
)

// Ingester runs the Ingestions, one at a time.
//
// Start records a new Ingestion and returns at once. Run, a long-running
// loop, does the work, and also resumes an Ingestion that was still running
// when the process stopped. Status, TopSenders and Changed are what the HTTP
// handlers read.
type Ingester struct {
	store   Store
	src     source.Source
	o       Options
	tracer  trace.Tracer
	metrics *metrics

	// starts wakes Run to run the current Ingestion. It has room for one
	// request, so requests made while one is waiting collapse into it.
	starts chan struct{}

	// startMu makes Start's "is one running?" check and its Begin one step,
	// so two Starts can't both begin. Run takes it too before it reads the
	// state, so it never works on an Ingestion that Start hasn't finished
	// announcing.
	startMu sync.Mutex

	// changed is closed after every write and replaced with a new channel,
	// which wakes everyone waiting on Changed once per write. changedMu
	// guards it.
	changedMu sync.Mutex
	changed   chan struct{}
}

// New returns an Ingester that reads the Source and records its progress
// in the Store. It fails only if its metrics can't be registered with
// opts.Registerer, for example because they already are.
func New(store Store, src source.Source, opts Options) (*Ingester, error) {
	m := newMetrics()
	if err := m.register(opts.Registerer); err != nil {
		return nil, fmt.Errorf("register metrics: %w", err)
	}

	return &Ingester{
		store:   store,
		src:     src,
		o:       opts,
		tracer:  otel.Tracer("mingest/pkg/ingest"),
		metrics: m,
		starts:  make(chan struct{}, 1),
		changed: make(chan struct{}),
	}, nil
}

// Start begins a new Ingestion and returns its Status at once; Run does the
// work. If an Ingestion is already running, Start returns that one's Status
// with ErrRunning, so the caller can show what is running. Start never
// opens the Source.
func (i *Ingester) Start(ctx context.Context) (Status, error) {
	i.startMu.Lock()
	defer i.startMu.Unlock()

	st, err := i.store.Status(ctx)
	if err != nil {
		return st, err
	}

	if st.State == StateRunning {
		return st, ErrRunning
	}

	st, err = i.store.Begin(ctx, Begin{
		ID:        uuid.NewV7().String(),
		SourceURL: i.o.SourceURL,
		At:        time.Now(),
	})
	if err != nil {
		return st, err
	}

	i.wrote(st)
	i.o.Logger.InfoContext(ctx, "ingestion started",
		slog.String("ingestion_id", st.ID))

	// Run is woken to run it. If a wake is already waiting, one is enough:
	// a run always picks up whichever Ingestion is current.
	select {
	case i.starts <- struct{}{}:
	default:
	}

	return st, nil
}

// Status returns the Status as of the last write.
func (i *Ingester) Status(ctx context.Context) (Status, error) {
	return i.store.Status(ctx)
}

// TopSenders returns Top Senders as of the last commit, with the Status
// they belong to.
func (i *Ingester) TopSenders(ctx context.Context) (TopSenders, error) {
	return i.store.TopSenders(ctx)
}

// Changed returns a channel that is closed at the next write. To follow the
// Status, take the channel first and then read Status, so a write between
// the two isn't missed.
func (i *Ingester) Changed() <-chan struct{} {
	i.changedMu.Lock()
	defer i.changedMu.Unlock()

	return i.changed
}

// Run is the Ingester's main loop, and blocks until ctx ends. It first
// resumes an Ingestion that was left running, then runs each Ingestion
// that Start begins, one at a time.
//
// When ctx ends mid-Ingestion, Run stops it without marking it failed: the
// state stays running, so the next process resumes it. Run returns nil
// once every goroutine it started has stopped.
func (i *Ingester) Run(ctx context.Context) error {
	for {
		i.ingest(ctx)

		select {
		case <-ctx.Done():
			return nil
		case <-i.starts:
		}
	}
}

// ingest runs the current Ingestion until it is done, fails, or ctx ends.
// It does nothing if no Ingestion is running.
func (i *Ingester) ingest(ctx context.Context) {
	st, ok := i.current(ctx)
	if !ok {
		return
	}

	// A waiting wake is discarded: this run answers every Start so far.
	// Otherwise a Start that came in as Run began, followed by a failed
	// Finish that leaves the state running, would run the same Ingestion
	// a second time.
	select {
	case <-i.starts:
	default:
	}

	log := i.o.Logger.With(slog.String("ingestion_id", st.ID))

	err := i.attempts(ctx, log)
	if ctx.Err() != nil {
		return
	}

	i.finish(ctx, log, err)
}

// current returns the running Ingestion, or false if none is running or
// ctx has ended (a request can still be waiting after a shutdown). It takes
// startMu, so it waits for a Start in progress: otherwise Run could write to
// the new Ingestion before Start has announced it. It also sets the progress
// gauges from the stored Status, so after a restart they show the last
// Ingestion, even one that is done.
func (i *Ingester) current(ctx context.Context) (Status, bool) {
	i.startMu.Lock()
	defer i.startMu.Unlock()

	st, err := i.store.Status(ctx)
	if err == nil {
		i.metrics.progress(st)
	}

	return st, err == nil && st.State == StateRunning && ctx.Err() == nil
}

// attempts runs the Pipeline, and runs it again after each Source error,
// waiting longer each time. Success or any other error ends the loop. It
// gives up with errSourceUnreachable once the Source has failed more than
// SourceRetries times in a row; an attempt that commits anything starts the
// count over.
func (i *Ingester) attempts(ctx context.Context, log *slog.Logger) error {
	failures := 0

	for {
		commits, err := i.attempt(ctx, log)
		if !errors.Is(err, errSource) {
			return err
		}

		if commits > 0 {
			failures = 0
		}

		failures++
		if err := i.backoff(ctx, log, failures, err); err != nil {
			return err
		}
	}
}

// backoff waits before retry number n, or returns errSourceUnreachable
// once n is past SourceRetries. The wait is random, between 0 and RetryBase
// doubled n-1 times, capped at RetryMax ("full jitter"), so clients that
// failed together don't retry in step. If ctx has ended it returns at once:
// the failure was most likely the shutdown itself.
func (i *Ingester) backoff(
	ctx context.Context,
	log *slog.Logger,
	n int,
	cause error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if n > i.o.SourceRetries {
		return fmt.Errorf("%w: %w", errSourceUnreachable, cause)
	}

	var wait time.Duration
	if bound := min(i.o.RetryBase<<(n-1), i.o.RetryMax); bound > 0 {
		wait = rand.N(bound) //nolint:gosec // a backoff, not a secret
	}

	log.WarnContext(ctx, "source failed, retrying", slog.Int("attempt", n),
		slog.Duration("wait", wait), slog.Any("error", cause))
	i.metrics.sourceRetries.Inc()

	t := time.NewTimer(wait)
	defer t.Stop()

	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// attempt opens the Source and runs the Pipeline over it once, skipping the
// Mail files already committed. It returns how many chunks it committed, so
// attempts can tell whether it made progress.
func (i *Ingester) attempt(ctx context.Context, log *slog.Logger) (int, error) {
	stream, st, err := i.open(ctx, log)
	if err != nil {
		return 0, err
	}
	// The Stream is only read, so an error from Close tells us nothing.
	defer func() { _ = stream.Close() }()

	commits := 0
	p := &pipeline{
		o:       i.o,
		store:   i.store,
		tracer:  i.tracer,
		logger:  log,
		metrics: i.metrics,
		committed: func(st Status) {
			commits++
			i.wrote(st)
		},
	}

	err = p.run(ctx, stream, st.MailFiles)

	return commits, err
}

// open opens the Source and records the open in the Store. On a resume it
// first checks that the Source is still the same archive.
func (i *Ingester) open(
	ctx context.Context,
	log *slog.Logger,
) (*source.Stream, Status, error) {
	stream, err := i.src.Open(ctx)
	if err != nil {
		return nil, Status{}, fmt.Errorf("%w: %w", errSource, err)
	}

	st, err := i.store.Status(ctx)
	if err == nil {
		err = sameSource(st, stream)
	}

	if err == nil {
		st, err = i.store.Opened(ctx, Opened{
			Size:    stream.Size,
			Version: stream.Version,
			At:      time.Now(),
		})
	}

	if err != nil {
		_ = stream.Close()
		return nil, Status{}, err
	}

	i.wrote(st)

	// The first open isn't a resume.
	if st.Resumes > 0 {
		log.InfoContext(ctx, "ingestion resumed",
			slog.Int64("resumes", st.Resumes),
			slog.Int64("mail_files", st.MailFiles))
	}

	return stream, st, nil
}

// sameSource returns errSourceChanged if a resume opened a different
// archive: one with another URL, size or version. The first open has
// nothing to compare against. Begin is the only write before it, so a
// Revision of 1 means the Source hasn't been opened yet. A Stream without
// a version can't be compared, so it never matches: resuming on it could
// mix two archives.
func sameSource(st Status, s *source.Stream) error {
	matches := s.Version != "" && s.Version == st.SourceVersion &&
		s.URL == st.SourceURL && s.Size == st.BytesTotal
	if st.Revision <= 1 || matches {
		return nil
	}

	return fmt.Errorf("%w: was %s (%d bytes, version %q), is %s (%d bytes, "+
		"version %q)", errSourceChanged, st.SourceURL, st.BytesTotal,
		st.SourceVersion, s.URL, s.Size, s.Version)
}

// finish records the end of the Ingestion: done if err is nil, failed
// otherwise. If the Store can't record
// it, the error is logged and the state stays running, so the next process
// resumes the Ingestion. A failure logs the full error, which /status
// leaves out.
func (i *Ingester) finish(ctx context.Context, log *slog.Logger, err error) {
	f := Finish{State: StateDone, At: time.Now()}
	if err != nil {
		f.State, f.Error = StateFailed, failure(err)
	}

	// A transient Store error gets finishAttempts tries, RetryBase apart and
	// growing, before the Ingestion is left running for the next process
	// to finish.
	st, ferr := i.store.Finish(ctx, f)
	for attempt := 1; ferr != nil && attempt < finishAttempts; attempt++ {
		select {
		case <-ctx.Done():
		case <-time.After(i.o.RetryBase * time.Duration(attempt)):
			st, ferr = i.store.Finish(ctx, f)
		}
	}

	if ferr != nil {
		log.ErrorContext(ctx, "ingestion finish failed",
			slog.Any("error", errors.Join(err, ferr)))

		return
	}

	i.wrote(st)

	if err != nil {
		log.ErrorContext(ctx, "ingestion failed",
			slog.String("code", string(st.Error.Code)),
			slog.Any("error", err))

		return
	}

	log.InfoContext(ctx, "ingestion done",
		slog.Int64("mail_files", st.MailFiles),
		slog.Int64("messages", st.Messages),
		slog.Int64("senders", st.Senders),
		slog.Duration("duration", st.FinishedAt.Sub(st.StartedAt)))
}

// wrote is called after every successful write. It updates the progress
// gauges and wakes everyone waiting on Changed.
func (i *Ingester) wrote(st Status) {
	i.metrics.progress(st)

	i.changedMu.Lock()
	defer i.changedMu.Unlock()

	close(i.changed)
	i.changed = make(chan struct{})
}

// failure is the Failure that /status shows for an Ingestion's error. The
// messages are fixed on purpose: the error itself can contain file paths or
// the Dataset's URL, which may carry credentials, so it only goes to the
// "ingestion failed" log line.
func failure(err error) Failure {
	switch {
	case errors.Is(err, ErrDiskFull):
		return Failure{
			CodeDiskFull,
			"The data volume has too little free space left.",
		}
	case errors.Is(err, errSourceChanged):
		return Failure{
			CodeSourceChanged,
			"The Dataset changed since the Ingestion started, or couldn't be checked.",
		}
	case errors.Is(err, errSourceUnreachable):
		return Failure{
			CodeSourceUnreachable,
			"The Dataset couldn't be read, retries included.",
		}
	case errors.Is(err, errArchiveCorrupt):
		return Failure{
			CodeArchiveCorrupt,
			"The Dataset isn't a valid gzipped tar.",
		}
	}

	return Failure{CodeInternal, "An unexpected error; the log has the cause."}
}
