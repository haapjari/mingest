package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"mingest/pkg/ingest"
)

// counts counts what a chunk adds.
type counts struct {
	messages, senders, withoutSender int64

	// bySender holds each Sender's new Messages.
	bySender map[string]int64
}

// Commit writes a chunk and its progress in one transaction: the new
// Messages, their Senders, and the counts. Below the disk reserve it
// returns ingest.ErrDiskFull without starting the transaction, so the
// reserve leaves room for Finish to record the failure.
func (d *DB) Commit(
	ctx context.Context,
	c ingest.Chunk,
) (ingest.Status, error) {
	start := time.Now()

	free, err := d.opts.FreeSpace(d.dir)
	if err != nil {
		return ingest.Status{}, fmt.Errorf("store: free space: %w", err)
	}

	if free < d.opts.DiskReserve {
		return ingest.Status{}, fmt.Errorf(
			"%w: %d bytes free, below the %d-byte reserve",
			ingest.ErrDiskFull, free, d.opts.DiskReserve)
	}

	st, err := d.write(ctx, func(tx *sql.Tx) (ingest.TopSenders, error) {
		return commitChunk(ctx, tx, c)
	})
	if err != nil {
		return ingest.Status{}, err
	}

	d.metrics.commitDuration.Observe(time.Since(start).Seconds())

	return st, nil
}

// commitChunk writes the chunk, then reads back the progress row and the
// top ten, still inside the transaction, so both match this commit.
func commitChunk(
	ctx context.Context,
	tx *sql.Tx,
	c ingest.Chunk,
) (ingest.TopSenders, error) {
	t, err := insertMessages(ctx, tx, c)
	if err != nil {
		return ingest.TopSenders{}, err
	}

	if err := t.upsertSenders(ctx, tx); err != nil {
		return ingest.TopSenders{}, err
	}

	// The progress row is updated in the same transaction as the inserts,
	// so a crash can never leave counts that disagree with the stored
	// Messages. The counts are the deltas of this Chunk only; the returned
	// Status is the running total read back from the row.
	st, err := scanStatus(tx.QueryRowContext(ctx, updateProgress,
		len(c.MailFiles), t.messages, t.senders, t.withoutSender,
		c.BytesRead, formatTime(c.At)))
	if err != nil {
		return ingest.TopSenders{}, fmt.Errorf(
			"store: update progress: %w",
			err,
		)
	}

	return readTop(ctx, tx, st)
}

// insertMessages inserts each Mail file's Message if it is absent, and
// tallies the new ones: a Mail file whose insert added a row is a new
// Message.
func insertMessages(
	ctx context.Context,
	tx *sql.Tx,
	c ingest.Chunk,
) (t *counts, err error) {
	stmt, err := tx.PrepareContext(ctx, insertMessage)
	if err != nil {
		return nil, fmt.Errorf("store: prepare: %w", err)
	}
	defer func() { err = errors.Join(err, stmt.Close()) }()

	t = &counts{bySender: make(map[string]int64)}

	for _, id := range c.MailFiles {
		res, err := stmt.ExecContext(ctx, id.Fingerprint[:])
		if err != nil {
			return nil, fmt.Errorf("store: insert message: %w", err)
		}

		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}

		t.messages++

		if id.Sender == "" {
			t.withoutSender++
		} else {
			t.bySender[id.Sender]++
		}
	}

	return t, nil
}

// upsertSenders adds each Sender's new Messages, in address order, and
// counts the new Senders: a Sender whose total is the chunk's is new.
func (t *counts) upsertSenders(ctx context.Context, tx *sql.Tx) (err error) {
	stmt, err := tx.PrepareContext(ctx, upsertSender)
	if err != nil {
		return fmt.Errorf("store: prepare: %w", err)
	}
	defer func() { err = errors.Join(err, stmt.Close()) }()

	for _, address := range slices.Sorted(maps.Keys(t.bySender)) {
		var total int64

		added := t.bySender[address]
		if err := stmt.QueryRowContext(ctx, address, added).
			Scan(&total); err != nil {
			return fmt.Errorf("store: upsert sender: %w", err)
		}

		if total == added {
			t.senders++
		}
	}

	return nil
}

// readTop reads the current top ten in the transaction, and returns them
// with the Status.
func readTop(
	ctx context.Context,
	tx *sql.Tx,
	st ingest.Status,
) (top ingest.TopSenders, err error) {
	rows, err := tx.QueryContext(ctx, selectTopSenders, ingest.TopSendersLimit)
	if err != nil {
		return ingest.TopSenders{}, fmt.Errorf("store: top senders: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()

	senders := make([]ingest.TopSender, 0, ingest.TopSendersLimit)
	for rows.Next() {
		var s ingest.TopSender
		if err := rows.Scan(&s.Sender, &s.Messages); err != nil {
			return ingest.TopSenders{}, fmt.Errorf(
				"store: top senders: %w",
				err,
			)
		}

		senders = append(senders, s)
	}

	if err := rows.Err(); err != nil {
		return ingest.TopSenders{}, fmt.Errorf("store: top senders: %w", err)
	}

	return ingest.TopSenders{Status: st, Senders: senders}, nil
}
