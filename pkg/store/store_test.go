package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"mingest/pkg/ingest"
	"mingest/pkg/message"
	"mingest/pkg/store"
)

const (
	ingestionID = "0190c4a4-8d4e-7b2a-9f3e-1a2b3c4d5e6f"
	sourceURL   = "https://example.com/enron.tar.gz"
)

// t0 is when every test's Ingestion begins.
var t0 = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func TestDB_Opened_RecordsSourceThenCountsResumes(t *testing.T) {
	t.Parallel()

	db := begun(t)

	first := write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Opened(
			ctx,
			ingest.Opened{Size: 1000, Version: `"v1"`, At: t0.Add(time.Second)},
		)
	})
	assert.Equal(t, int64(1000), first.BytesTotal)
	assert.Equal(t, `"v1"`, first.SourceVersion)
	assert.Zero(t, first.Resumes)
	assert.Equal(t, int64(2), first.Revision)
	assert.Equal(t, t0.Add(time.Second), first.UpdatedAt)

	again := write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Opened(
			ctx,
			ingest.Opened{
				Size:    1000,
				Version: `"v1"`,
				At:      t0.Add(2 * time.Second),
			},
		)
	})
	assert.Equal(t, int64(1), again.Resumes)
	assert.Equal(t, int64(3), again.Revision)
	assert.Equal(t, t0.Add(2*time.Second), again.UpdatedAt)
}

func TestDB_Opened_RecordsUnknownSizeOrVersion(t *testing.T) {
	t.Parallel()

	db := begun(t)

	got := write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Opened(ctx, ingest.Opened{Size: -1, At: t0})
	})
	assert.Equal(t, int64(-1), got.BytesTotal)
	assert.Empty(t, got.SourceVersion)

	// An empty Version is still an open, so the next one is a resume.
	again := write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Opened(ctx, ingest.Opened{Size: -1, At: t0})
	})
	assert.Equal(t, int64(1), again.Resumes)
}

func TestDB_Commit_CountsMailFilesOfOnlyCopies(t *testing.T) {
	t.Parallel()

	db := begun(t)
	commit(t, db, 100, mailFile(1, "a@enron.com"))

	got := commit(t, db, 200, mailFile(1, "a@enron.com"))
	assert.Equal(t, int64(2), got.MailFiles)
	assert.Equal(t, int64(1), got.Messages)
	assert.Equal(t, int64(1), got.Senders)
}

func TestDB_Writes_FailWithoutIngestion(t *testing.T) {
	t.Parallel()

	db := openStore(t, t.TempDir())
	ctx := context.Background()

	_, err := db.Opened(ctx, ingest.Opened{Size: 1, At: t0})
	require.ErrorIs(t, err, store.ErrNoIngestion)

	_, err = db.Commit(ctx, ingest.Chunk{At: t0})
	require.ErrorIs(t, err, store.ErrNoIngestion)

	_, err = db.Finish(ctx, ingest.Finish{State: ingest.StateDone, At: t0})
	require.ErrorIs(t, err, store.ErrNoIngestion)

	assert.Equal(t, ingest.StateIdle, status(t, db).State)
}

func TestDB_Commit_ChangesNothingWhenFailingAtAnyStep(t *testing.T) {
	t.Parallel()

	// Each breaks one step of a commit through a second connection, and
	// repairs the schema before the file is reopened.
	const dropTrigger = `DROP TRIGGER fail`

	tests := map[string]struct{ breakage, repair string }{
		"inserting a Message": {
			breakage: `CREATE TRIGGER fail BEFORE INSERT ON message
				BEGIN SELECT RAISE(ABORT, 'injected'); END`,
			repair: dropTrigger,
		},
		"preparing the Message insert": {
			breakage: `DROP TABLE message`,
			repair: `CREATE TABLE message (fingerprint BLOB PRIMARY KEY)
				WITHOUT ROWID`,
		},
		"adding a new Sender": {
			breakage: `CREATE TRIGGER fail BEFORE INSERT ON sender
				BEGIN SELECT RAISE(ABORT, 'injected'); END`,
			repair: dropTrigger,
		},
		"preparing the Sender upsert": {
			breakage: `DROP TABLE sender`,
			repair: `CREATE TABLE sender (address TEXT PRIMARY KEY,
				messages INTEGER NOT NULL) WITHOUT ROWID`,
		},
		"updating the progress": {
			breakage: `CREATE TRIGGER fail
				BEFORE UPDATE OF mail_files ON ingestion
				BEGIN SELECT RAISE(ABORT, 'injected'); END`,
			repair: dropTrigger,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			db := beginIn(t, dir)
			side := sideConnection(t, dir)
			exec(t, side, tc.breakage)

			// Opened is a write too, so the Store sees the new schema
			// before the commit, as it would on a real resume.
			before := opened(t, db)

			_, err := db.Commit(
				context.Background(),
				chunk(100, mailFile(1, "a@enron.com")),
			)
			require.Error(t, err)
			assert.Equal(t, before, status(t, db), "the snapshot")

			exec(t, side, tc.repair)
			assert.Equal(t, before, reopen(t, db, dir), "the file")
		})
	}
}

func TestDB_Commit_ChangesNothingWhenCancelled(t *testing.T) {
	t.Parallel()

	db := begun(t)
	before := status(t, db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := db.Commit(ctx, chunk(100, mailFile(1, "a@enron.com")))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, before, status(t, db))
}

func TestDB_Begin_SwitchesToNewIngestion(t *testing.T) {
	t.Parallel()

	db := begun(t)
	commit(t, db, 100, mailFile(1, "a@enron.com"))

	const next = "0190c4a5-0000-7000-8000-000000000000"

	got := write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Begin(
			ctx,
			ingest.Begin{ID: next, SourceURL: sourceURL, At: t0.Add(time.Hour)},
		)
	})
	assert.Equal(t, next, got.ID)
	assert.Equal(t, int64(1), got.Revision)
	assert.Zero(t, got.Messages)

	// The first Ingestion's Message is new to the second.
	assert.Equal(
		t,
		int64(1),
		commit(t, db, 100, mailFile(1, "a@enron.com")).Messages,
	)
}

func TestDB_Begin_KeepsCurrentIngestionWhenFailing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := beginIn(t, dir)
	before := commit(t, db, 100, mailFile(1, "a@enron.com"))

	// The Ingestion's file already exists, so Begin can't create it, and
	// must leave it alone.
	_, err := db.Begin(
		context.Background(),
		ingest.Begin{ID: ingestionID, SourceURL: sourceURL, At: t0},
	)
	require.ErrorIs(t, err, os.ErrExist)
	assert.Equal(t, before, status(t, db))
	assert.Equal(t, before, reopen(t, db, dir), "the file")
}

func TestOpen_FailsOnUnreadableFile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setup func(t *testing.T, dir string)
		check func(t *testing.T, err error)
	}{
		"not a database": {
			setup: func(t *testing.T, dir string) {
				path := filepath.Join(dir, ingestionID+".db")
				require.NoError(
					t,
					os.WriteFile(path, []byte("not a database"), 0o600),
				)
			},
			check: func(t *testing.T, err error) {
				var se *sqlite.Error
				require.ErrorAs(t, err, &se)
				assert.Equal(t, sqlite3.SQLITE_NOTADB, se.Code())
			},
		},
		"a malformed time": {
			setup: func(t *testing.T, dir string) {
				beginAndClose(t, dir, ingestionID)
				exec(
					t,
					sideConnection(t, dir),
					`UPDATE ingestion SET started_at = 'noon'`,
				)
			},
			check: func(t *testing.T, err error) {
				var pe *time.ParseError
				assert.ErrorAs(t, err, &pe)
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			tt.setup(t, dir)

			db, err := store.Open(dir, store.Options{})
			tt.check(t, err)
			assert.Nil(t, db)
		})
	}
}

// BenchmarkCommit commits 1,000-Mail-file chunks into a growing table.
// The size is the database file's once Close has checkpointed the WAL into
// it.
func BenchmarkCommit(b *testing.B) {
	dir := b.TempDir()

	db, err := store.Open(dir, store.Options{})
	require.NoError(b, err)

	ctx := context.Background()
	_, err = db.Begin(
		ctx,
		ingest.Begin{ID: ingestionID, SourceURL: sourceURL, At: t0},
	)
	require.NoError(b, err)

	const perChunk = 1000

	var n uint64

	files := make([]message.Identity, perChunk)

	for b.Loop() {
		for i := range files {
			n++
			files[i] = randomMailFile(n)
		}

		_, err := db.Commit(ctx, ingest.Chunk{MailFiles: files, At: t0})
		require.NoError(b, err)
	}

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "commits/s")

	require.NoError(b, db.Close())
	b.ReportMetric(float64(fileSize(b, dir))/float64(n), "bytes/message")
}

// openStore opens the Store in dir, with opts if given, and closes it when
// the test ends.
func openStore(tb testing.TB, dir string, opts ...store.Options) *store.DB {
	tb.Helper()

	var o store.Options
	if len(opts) > 0 {
		o = opts[0]
	}

	db, err := store.Open(dir, o)
	require.NoError(tb, err)
	tb.Cleanup(func() { assert.NoError(tb, db.Close()) })

	return db
}

// begun opens a Store in a new directory and begins an Ingestion.
func begun(t *testing.T) *store.DB {
	t.Helper()

	return beginIn(t, t.TempDir())
}

// beginIn opens a Store in dir and begins an Ingestion.
func beginIn(t *testing.T, dir string) *store.DB {
	t.Helper()

	db := openStore(t, dir)
	_, err := db.Begin(context.Background(), ingest.Begin{
		ID:        ingestionID,
		SourceURL: sourceURL,
		At:        t0,
	})
	require.NoError(t, err)

	return db
}

// opened records an open of a 1,000-byte Source.
func opened(t *testing.T, db *store.DB) ingest.Status {
	t.Helper()

	return write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Opened(
			ctx,
			ingest.Opened{Size: 1000, Version: `"v1"`, At: t0},
		)
	})
}

// write runs one write, and checks that the snapshot is what it returned.
func write(
	t *testing.T,
	db *store.DB,
	fn func(ctx context.Context) (ingest.Status, error),
) ingest.Status {
	t.Helper()

	got, err := fn(context.Background())
	require.NoError(t, err)
	assert.Equal(t, got, status(t, db), "the snapshot after the write")

	return got
}

func commit(
	t *testing.T,
	db *store.DB,
	bytesRead int64,
	files ...message.Identity,
) ingest.Status {
	t.Helper()

	return write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Commit(ctx, chunk(bytesRead, files...))
	})
}

// chunk is a Chunk cut after bytesRead bytes, at a time that follows them.
func chunk(bytesRead int64, files ...message.Identity) ingest.Chunk {
	return ingest.Chunk{
		MailFiles: files,
		BytesRead: bytesRead,
		At:        chunkTime(bytesRead),
	}
}

func chunkTime(bytesRead int64) time.Time {
	return t0.Add(time.Duration(bytesRead) * time.Millisecond)
}

func status(tb testing.TB, db *store.DB) ingest.Status {
	tb.Helper()

	got, err := db.Status(context.Background())
	require.NoError(tb, err)

	return got
}

// mailFile is a Mail file whose Message's fingerprint is n.
func mailFile(n uint64, sender string) message.Identity {
	id := message.Identity{Sender: sender}
	binary.LittleEndian.PutUint64(id.Fingerprint[:], n)

	return id
}

// randomMailFile is the benchmark's n-th Mail file: a fingerprint as
// uniform as a real one, and about one Sender per 25 Messages.
func randomMailFile(n uint64) message.Identity {
	sum := sha256.Sum256(binary.LittleEndian.AppendUint64(nil, n))

	return message.Identity{
		Fingerprint: [16]byte(sum[:16]),
		Sender:      fmt.Sprintf("sender%d@enron.com", n/25),
	}
}

// reopen closes the Store and returns the Status a new Store reads from
// dir.
func reopen(t *testing.T, db *store.DB, dir string) ingest.Status {
	t.Helper()

	require.NoError(t, db.Close())

	return status(t, openStore(t, dir))
}

// sideConnection opens the Ingestion's file on a second connection.
func sideConnection(t *testing.T, dir string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(dir, ingestionID+".db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })

	return db
}

func exec(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	_, err := db.ExecContext(context.Background(), query)
	require.NoError(t, err)
}

// fileSize is the bytes of the database files in dir.
func fileSize(tb testing.TB, dir string) int64 {
	tb.Helper()

	paths, err := filepath.Glob(filepath.Join(dir, "*.db*"))
	require.NoError(tb, err)

	var n int64
	for _, path := range paths {
		info, err := os.Stat(path)
		require.NoError(tb, err)

		n += info.Size()
	}

	return n
}
