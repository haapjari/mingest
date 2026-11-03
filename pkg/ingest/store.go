package ingest

import (
	"context"
	"errors"
	"time"

	"mingest/pkg/message"
)

// ErrDiskFull is a write the Store refused or rolled back because the disk
// is full. The last committed chunk stays intact.
var ErrDiskFull = errors.New("disk full")

// Store keeps the Ingestion: its Messages, its Senders and its progress.
// Each write returns the Status it committed.
type Store interface {
	// Begin starts a new Ingestion in state running, in place of the
	// current one.
	Begin(ctx context.Context, b Begin) (Status, error)

	// Opened records an open of the Source. The first open records its
	// size and version; every later one counts as a resume.
	Opened(ctx context.Context, o Opened) (Status, error)

	// Commit writes a chunk and its progress in one transaction.
	Commit(ctx context.Context, c Chunk) (Status, error)

	// Finish ends the Ingestion, done or failed.
	Finish(ctx context.Context, f Finish) (Status, error)

	// Status returns the last committed Status, idle when there is no
	// Ingestion.
	Status(ctx context.Context) (Status, error)

	// TopSenders returns the Top Senders of the last commit, with the
	// Status they belong to.
	TopSenders(ctx context.Context) (TopSenders, error)
}

// TopSendersLimit is how many Senders Top Senders lists. Ten is part of
// what Top Senders means, not a setting.
const TopSendersLimit = 10

// TopSenders is the Senders with the most Messages, as of one commit.
type TopSenders struct {
	// Status is the Status of the commit the list is from.
	Status Status

	// Senders holds at most TopSendersLimit Senders, most Messages first;
	// ties go in address order. While running, the counts are so far; while
	// idle, the list is empty.
	Senders []TopSender
}

// TopSender is one Sender and its Messages.
type TopSender struct {
	Sender   string
	Messages int64
}

// State is where an Ingestion is in its lifecycle.
type State string

// The States. Idle means there is no Ingestion yet.
const (
	StateIdle    State = "idle"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// Code says why an Ingestion failed.
type Code string

// The failure Codes.
const (
	CodeDiskFull          Code = "disk_full"
	CodeSourceUnreachable Code = "source_unreachable"
	CodeSourceChanged     Code = "source_changed"
	CodeArchiveCorrupt    Code = "archive_corrupt"
	CodeInternal          Code = "internal"
)

// Status is the progress row of an Ingestion.
type Status struct {
	ID    string
	State State

	MailFiles             int64
	Messages              int64
	Senders               int64
	MessagesWithoutSender int64

	// BytesRead is the compressed bytes read up to the last commit.
	BytesRead int64

	// BytesTotal is the Source's size, or -1 while it is unknown.
	BytesTotal int64

	// Resumes counts the opens of the Source after the first.
	Resumes int64

	// Revision goes up by one on every write.
	Revision int64

	// StartedAt, UpdatedAt and FinishedAt are zero until set.
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt time.Time

	// Error is set when the State is failed.
	Error Failure

	// The Source, as Begin and Opened recorded it. They are never served.
	SourceURL     string
	SourceVersion string
}

// Failure is why an Ingestion failed.
type Failure struct {
	Code    Code
	Message string
}

// Begin starts an Ingestion.
type Begin struct {
	ID        string
	SourceURL string
	At        time.Time
}

// Opened is one open of the Source.
type Opened struct {
	// Size is -1 when the Source can't tell.
	Size    int64
	Version string
	At      time.Time
}

// Chunk is the Mail files committed in one transaction.
type Chunk struct {
	MailFiles []message.Identity

	// BytesRead is the compressed bytes read when the chunk was cut.
	BytesRead int64
	At        time.Time
}

// Finish ends an Ingestion.
type Finish struct {
	// State is done or failed.
	State State

	// Error is set when the State is failed.
	Error Failure
	At    time.Time
}
