package ingest

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"mingest/pkg/message"
)

// fakeStore keeps the Status in memory the way the real Store counts it,
// and records every committed chunk. Its hooks, when set, run before each
// write and can fail it.
type fakeStore struct {
	mu      sync.Mutex
	status  Status
	chunks  []Chunk
	seen    map[[16]byte]bool
	senders map[string]int64

	// sourceKnown is set by the first Opened, as source_version is.
	sourceKnown bool

	onOpened func(ctx context.Context, o Opened) error
	onCommit func(ctx context.Context, c Chunk) error
	onFinish func(ctx context.Context, f Finish) error

	// topSendersErr, when set, is what TopSenders fails with.
	topSendersErr error

	// statusFails is how many of the next Status calls fail with
	// errStatus. They still return the Status, as a careless caller
	// would read it.
	statusFails int

	// afterWrite runs at the end of each write, under the lock, and
	// afterCommit after each commit, outside it.
	afterWrite  func()
	afterCommit func()
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		status:  Status{State: StateIdle, BytesTotal: -1},
		seen:    map[[16]byte]bool{},
		senders: map[string]int64{},
	}
}

func (s *fakeStore) Begin(_ context.Context, b Begin) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = Status{
		ID:         b.ID,
		State:      StateRunning,
		BytesTotal: -1,
		StartedAt:  b.At,
		UpdatedAt:  b.At,
		SourceURL:  b.SourceURL,
	}
	s.chunks = nil
	s.sourceKnown = false
	s.seen = map[[16]byte]bool{}
	s.senders = map[string]int64{}

	return s.write(), nil
}

func (s *fakeStore) Opened(ctx context.Context, o Opened) (Status, error) {
	if s.onOpened != nil {
		if err := s.onOpened(ctx, o); err != nil {
			return Status{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sourceKnown {
		s.status.Resumes++
	} else {
		s.status.BytesTotal = o.Size
		s.status.SourceVersion = o.Version
		s.sourceKnown = true
	}

	s.status.UpdatedAt = o.At

	return s.write(), nil
}

func (s *fakeStore) Commit(ctx context.Context, c Chunk) (Status, error) {
	if s.onCommit != nil {
		if err := s.onCommit(ctx, c); err != nil {
			return Status{}, err
		}
	}

	st := s.commit(c)
	if s.afterCommit != nil {
		s.afterCommit()
	}

	return st, nil
}

func (s *fakeStore) commit(c Chunk) Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.chunks = append(s.chunks, c)
	for _, id := range c.MailFiles {
		s.count(id)
	}

	s.status.BytesRead = c.BytesRead
	s.status.UpdatedAt = c.At

	return s.write()
}

func (s *fakeStore) Finish(ctx context.Context, f Finish) (Status, error) {
	if s.onFinish != nil {
		if err := s.onFinish(ctx, f); err != nil {
			return Status{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.status.State = f.State
	s.status.Error = f.Error
	s.status.FinishedAt = f.At
	s.status.UpdatedAt = f.At

	return s.write(), nil
}

func (s *fakeStore) Status(context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.statusFails > 0 {
		s.statusFails--
		return s.status, errStatus
	}

	return s.status, nil
}

// failStatus makes the next n Status calls fail.
func (s *fakeStore) failStatus(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.statusFails = n
}

// errStatus is the error of a failing Status.
var errStatus = errors.New("status failed")

// TopSenders ranks every Sender, Messages descending, then Sender
// ascending, and keeps the first TopSendersLimit.
func (s *fakeStore) TopSenders(context.Context) (TopSenders, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.topSendersErr != nil {
		return TopSenders{}, s.topSendersErr
	}

	out := make([]TopSender, 0, len(s.senders))
	for sender, n := range s.senders {
		out = append(out, TopSender{Sender: sender, Messages: n})
	}

	slices.SortFunc(out, func(a, b TopSender) int {
		return cmp.Or(cmp.Compare(b.Messages, a.Messages),
			strings.Compare(a.Sender, b.Sender))
	})

	return TopSenders{
		Status:  s.status,
		Senders: out[:min(len(out), TopSendersLimit)],
	}, nil
}

// committed returns the chunks committed so far.
func (s *fakeStore) committed() []Chunk {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Chunk(nil), s.chunks...)
}

func (s *fakeStore) count(id message.Identity) {
	s.status.MailFiles++
	if s.seen[id.Fingerprint] {
		return
	}

	s.seen[id.Fingerprint] = true
	s.status.Messages++

	if id.Sender == "" {
		s.status.MessagesWithoutSender++
		return
	}

	if s.senders[id.Sender] == 0 {
		s.status.Senders++
	}

	s.senders[id.Sender]++
}

func (s *fakeStore) write() Status {
	s.status.Revision++
	if s.afterWrite != nil {
		s.afterWrite()
	}

	return s.status
}
