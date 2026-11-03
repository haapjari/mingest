package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"mingest/pkg/ingest"
)

// ErrNoIngestion is a write before any Ingestion has begun.
var ErrNoIngestion = errors.New("store: no ingestion")

// Options configures the Store.
type Options struct {
	// DiskReserve is the free space a commit must leave in the data
	// directory. Below it, Commit returns ingest.ErrDiskFull.
	DiskReserve int64

	// FreeSpace returns the free bytes in a directory. Nil means statfs.
	FreeSpace func(dir string) (int64, error)

	// HeapLimit is SQLite's hard heap limit in bytes, outside the Go heap.
	// The page cache is at most 2 MiB below it.
	HeapLimit int64

	// Registerer gets the Store's metrics. Nil leaves them unregistered.
	Registerer prometheus.Registerer
}

// DB is the Store: the current Ingestion's database file, written through
// one connection. Status and TopSenders never query SQLite: each write
// publishes a snapshot of the progress row and the top ten, and they read
// that.
type DB struct {
	dir  string
	opts Options
	lock *os.File

	// mu serializes the writes, so a snapshot is never published out of
	// order and Begin's switch of files never races a commit.
	mu     sync.Mutex
	writer *sql.DB

	// path is the current file, read by the size gauge without mu.
	path atomic.Pointer[string]

	snapshot atomic.Pointer[ingest.TopSenders]
	metrics  *metrics
}

var _ ingest.Store = (*DB)(nil)

// Open opens the Store in dir, creating dir if it is missing, and locks it
// against other processes until Close. The newest database file there is
// the current Ingestion, and its progress row seeds the snapshot; older
// ones, left by a crash during Begin, are deleted. With none, the Store is
// idle until Begin.
func Open(dir string, opts Options) (*DB, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	lock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}

	if opts.FreeSpace == nil {
		opts.FreeSpace = freeSpace
	}

	d := &DB{dir: dir, opts: opts, lock: lock}
	d.path.Store(new(string))
	d.publish(ingest.TopSenders{
		Status:  ingest.Status{State: ingest.StateIdle, BytesTotal: -1},
		Senders: []ingest.TopSender{},
	})
	d.metrics = newMetrics(d.fileBytes)

	if err := d.load(); err != nil {
		return nil, errors.Join(err, d.Close())
	}

	return d, nil
}

// load makes the newest file the current Ingestion and publishes its
// progress row, then deletes the older files and registers the metrics.
// With no file, the Store stays idle. A newest file without the schema was
// left by a crash inside Begin, before its one transaction committed: it's
// deleted, and the file before it is current again.
func (d *DB) load() error {
	path, stale, err := current(d.dir)
	if err != nil {
		return err
	}

	if path != "" {
		writer, top, err := openFile(
			context.Background(), path, d.opts.HeapLimit)
		if errors.Is(err, errUnfinished) {
			if err := removeFiles(path); err != nil {
				return err
			}

			return d.load()
		}

		if err != nil {
			return err
		}

		d.writer = writer
		d.path.Store(&path)
		d.publish(top)
	}

	if err := removeFiles(stale...); err != nil {
		return err
	}

	return d.metrics.register(d.opts.Registerer)
}

// Close closes the database file and releases the lock. Closing twice is a
// no-op.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	err := closeWriter(d.writer)
	if d.lock != nil {
		err = errors.Join(err, d.lock.Close())
	}

	d.writer, d.lock = nil, nil

	return err
}

// Status returns the snapshot. It never touches SQLite, so it can't fail.
func (d *DB) Status(_ context.Context) (ingest.Status, error) {
	return d.snapshot.Load().Status, nil
}

// TopSenders returns the snapshot's top ten, with the Status of the same
// commit. The list is a copy, so a caller can't change what others read.
func (d *DB) TopSenders(_ context.Context) (ingest.TopSenders, error) {
	top := *d.snapshot.Load()
	top.Senders = slices.Clone(top.Senders)

	return top, nil
}

func (d *DB) publish(top ingest.TopSenders) {
	d.snapshot.Store(&top)
}

// carry is for writes that don't change the Senders, Opened and Finish: it
// returns their new Status with the top ten from the previous snapshot.
// Call it with mu held.
func (d *DB) carry(st ingest.Status, err error) (ingest.TopSenders, error) {
	return ingest.TopSenders{
		Status:  st,
		Senders: d.snapshot.Load().Senders,
	}, err
}

// write runs fn in one transaction on the current file. Once the
// transaction commits, it publishes the snapshot fn returned, and returns
// that snapshot's Status.
func (d *DB) write(
	ctx context.Context,
	fn func(tx *sql.Tx) (ingest.TopSenders, error),
) (ingest.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.writer == nil {
		return ingest.Status{}, ErrNoIngestion
	}

	top, err := inTx(ctx, d.writer, nil, fn)
	if err != nil {
		return ingest.Status{}, diskFull(err)
	}

	d.publish(top)

	return top.Status, nil
}

// inTx runs fn in a transaction with opts. It commits if fn succeeds, and
// rolls back otherwise.
func inTx[T any](
	ctx context.Context,
	db *sql.DB,
	opts *sql.TxOptions,
	fn func(tx *sql.Tx) (T, error),
) (T, error) {
	var zero T

	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return zero, fmt.Errorf("store: begin: %w", err)
	}

	v, err := fn(tx)
	if err != nil {
		return zero, errors.Join(err, tx.Rollback())
	}

	// v is returned only if the commit succeeded.
	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("store: commit: %w", err)
	}

	return v, nil
}

// diskFull marks SQLITE_FULL as ingest.ErrDiskFull. SQLite has rolled the
// transaction back, so the last commit is intact.
func diskFull(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == sqlite3.SQLITE_FULL {
		return fmt.Errorf("%w: %w", ingest.ErrDiskFull, err)
	}

	return err
}
