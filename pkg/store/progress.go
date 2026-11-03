package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"mingest/pkg/ingest"
)

// Begin creates the new Ingestion's database file, with its schema and
// its progress row in state running, and makes it the current one. Then
// it closes the old file and deletes it. Once the new file is current,
// Begin has succeeded: an old file it can't close or delete is left for
// Open, which keeps the newest file and deletes the rest. A crash in
// between leaves both the same way, and a crash before the new file's
// transaction commits leaves a file without the schema, which Open deletes.
func (d *DB) Begin(ctx context.Context, b ingest.Begin) (ingest.Status, error) {
	path := fileName(d.dir, b.ID)

	writer, st, err := createFile(ctx, path, b, d.opts.HeapLimit)
	if err != nil {
		return ingest.Status{}, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	old, oldPath := d.writer, *d.path.Load()
	d.writer = writer
	d.path.Store(&path)
	d.publish(ingest.TopSenders{Status: st, Senders: []ingest.TopSender{}})

	_ = closeWriter(old)
	if oldPath != "" {
		_ = removeFiles(oldPath)
	}

	return st, nil
}

// Opened records the Source's size and version on its first open, and
// another resume on every later one.
func (d *DB) Opened(
	ctx context.Context,
	o ingest.Opened,
) (ingest.Status, error) {
	return d.write(ctx, func(tx *sql.Tx) (ingest.TopSenders, error) {
		return d.carry(scanStatus(
			tx.QueryRowContext(
				ctx,
				updateOpened,
				nullSize(o.Size),
				o.Version,
				formatTime(o.At)),
		))
	})
}

// Finish ends the Ingestion in state done or failed.
func (d *DB) Finish(
	ctx context.Context,
	f ingest.Finish,
) (ingest.Status, error) {
	return d.write(ctx, func(tx *sql.Tx) (ingest.TopSenders, error) {
		return d.carry(scanStatus(tx.QueryRowContext(
			ctx,
			updateFinish,
			string(f.State),
			nullString(string(f.Error.Code)),
			nullString(f.Error.Message),
			formatTime(f.At),
			formatTime(f.At))))
	})
}

// createFile creates a database file with the schema and the progress row.
// It claims the path first, so it never opens a file that already exists,
// and deletes what it created if anything after that fails.
func createFile(
	ctx context.Context,
	path string,
	b ingest.Begin,
	heapLimit int64,
) (*sql.DB, ingest.Status, error) {
	// The path is claimed first: an empty file, which SQLite takes as an
	// empty database, created only if none is there already.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, ingest.Status{}, fmt.Errorf("store: %w", err)
	}

	if err := f.Close(); err != nil {
		return nil, ingest.Status{}, fmt.Errorf("store: %w", err)
	}

	db := connect(path, heapLimit)

	st, err := inTx(ctx, db, nil, func(tx *sql.Tx) (ingest.Status, error) {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return ingest.Status{}, err
		}

		at := formatTime(b.At)

		return scanStatus(tx.QueryRowContext(ctx, insertProgress,
			b.ID, b.SourceURL, at, at))
	})
	if err != nil {
		return nil, ingest.Status{}, errors.Join(
			fmt.Errorf("store: create %s: %w", path, diskFull(err)),
			db.Close(),
			removeFiles(path),
		)
	}

	return db, st, nil
}

func closeWriter(db *sql.DB) error {
	if db == nil {
		return nil
	}

	return db.Close()
}
