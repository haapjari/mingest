package store_test

import (
	"context"
	"errors"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/ingest"
	"mingest/pkg/message"
	"mingest/pkg/store"
)

// Ingestion ids in time order, as UUIDv7s sort.
const (
	olderID = "0190c4a3-0000-7000-8000-000000000000"
	newerID = "0190c4a5-0000-7000-8000-000000000000"
)

func TestOpen_KeepsOnlyNewestFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	newest := beginAndClose(t, dir, newerID)

	stale := []string{
		olderID + ".db", olderID + ".db-wal", olderID + ".db-shm",
		ingestionID + ".db",
	}
	others := []string{"notes.txt", "backup.db", "lock"}

	for _, name := range append(stale, others...) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}

	assert.Equal(t, newest, status(t, openStore(t, dir)))

	for _, name := range stale {
		assert.NoFileExists(t, filepath.Join(dir, name))
	}

	for _, name := range others {
		assert.FileExists(t, filepath.Join(dir, name))
	}
}

func TestOpen_TakesNewFileAfterCrashMidBegin(t *testing.T) {
	t.Parallel()

	// The crash left both files: Begin had created the new one but not yet
	// deleted the old.
	dir := t.TempDir()
	beginAndClose(t, dir, olderID)

	other := t.TempDir()
	newest := beginAndClose(t, other, newerID)
	copyFile(
		t,
		filepath.Join(other, newerID+".db"),
		filepath.Join(dir, newerID+".db"),
	)

	assert.Equal(t, newest, status(t, openStore(t, dir)))
	assert.NoFileExists(t, filepath.Join(dir, olderID+".db"))
}

func TestOpen_KeepsOldFileAfterCrashBeforeBeginCommits(t *testing.T) {
	t.Parallel()

	// The crash left the new file as Begin claims it: empty.
	dir := t.TempDir()
	old := beginAndClose(t, dir, olderID)
	unfinished := filepath.Join(dir, newerID+".db")
	require.NoError(t, os.WriteFile(unfinished, nil, 0o600))

	assert.Equal(t, old, status(t, openStore(t, dir)))
	assert.NoFileExists(t, unfinished)
}

func TestDB_Begin_DeletesOldFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := openStore(t, dir)
	beginWith(t, db, olderID)
	beginWith(t, db, newerID)

	assert.NoFileExists(t, filepath.Join(dir, olderID+".db"))
	assert.NoFileExists(t, filepath.Join(dir, olderID+".db-wal"))
	assert.NoFileExists(t, filepath.Join(dir, olderID+".db-shm"))
	assert.FileExists(t, filepath.Join(dir, newerID+".db"))
}

func TestDB_Status_ShowsOneIngestionOrOtherDuringBegin(t *testing.T) {
	t.Parallel()

	db := openStore(t, t.TempDir())
	beginWith(t, db, olderID)

	stop := watchStatus(t, db)
	beginWith(t, db, newerID)
	seen := stop()

	assert.Subset(t, []string{olderID, newerID}, seen)
	assert.Equal(t, newerID, status(t, db).ID, "after Begin returns")
}

func TestDB_Commit_StartsNoTransactionBelowReserve(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := withFreeSpace(t, dir, reserve-1)
	before := status(t, db)

	// A commit that began a transaction would wait for this lock.
	holdWriteLock(t, dir)

	start := time.Now()
	_, err := db.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.ErrorIs(t, err, ingest.ErrDiskFull)

	assert.Less(t, time.Since(start), time.Second, "waited for the write lock")
	assert.Equal(t, before, status(t, db))
}

func TestDB_Commit_FailsWhenFreeSpaceUnknown(t *testing.T) {
	t.Parallel()

	errStatfs := errors.New("statfs failed")
	db := openStore(t, t.TempDir(), store.Options{
		FreeSpace: func(string) (int64, error) { return 0, errStatfs },
	})
	beginWith(t, db, ingestionID)

	_, err := db.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.ErrorIs(t, err, errStatfs)
}

func TestDB_Commit_ReportsDiskFullOnFullDatabase(t *testing.T) {
	t.Parallel()

	db, last := fullStore(t, t.TempDir())

	_, err := db.Commit(context.Background(), chunk(200, newMailFiles(5000)...))
	require.ErrorIs(t, err, ingest.ErrDiskFull)
	assert.Equal(t, last, status(t, db))
}

func TestDB_Finish_RecordsFailureAfterDiskFullCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, last := fullStore(t, dir)

	_, err := db.Commit(context.Background(), chunk(200, newMailFiles(5000)...))
	require.ErrorIs(t, err, ingest.ErrDiskFull)

	failed := finishDiskFull(t, db)

	reopened := reopen(t, db, dir)
	assert.Equal(t, failed, reopened)
	assert.Equal(
		t,
		last.Messages,
		reopened.Messages,
		"the last chunk is intact",
	)
}

func TestOpen_SetsCacheBelowHeapLimit(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		heapLimit int64
		wantKiB   int64
	}{
		"capped at 2 MiB":        {32 * 1024 * 1024, -2 * 1024},
		"7/8 of a smaller limit": {1024 * 1024, -896},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			db := openStore(t, t.TempDir(),
				store.Options{HeapLimit: tt.heapLimit})
			beginWith(t, db, ingestionID)

			// A negative cache_size is in KiB.
			kib, err := store.CacheSize(db)
			require.NoError(t, err)
			assert.Equal(t, tt.wantKiB, kib)
		})
	}
}

func TestDB_Commit_ChecksRealFreeSpace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := openStore(t, dir, store.Options{DiskReserve: 1})
	beginWith(t, db, ingestionID)
	commit(t, db, 100, mailFile(1, "a@enron.com"))

	full := openStore(t, t.TempDir(), store.Options{DiskReserve: math.MaxInt64})
	beginWith(t, full, ingestionID)

	_, err := full.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.ErrorIs(t, err, ingest.ErrDiskFull)
}

func TestDB_Commit_FailsWhenDataDirectoryIsGone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db := openStore(t, dir)
	beginWith(t, db, ingestionID)
	require.NoError(t, os.RemoveAll(dir))

	_, err := db.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestOpen_ReleasesLockWhenItFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(dir, ingestionID+".db"),
			[]byte("not a database"),
			0o600,
		),
	)

	_, err := store.Open(dir, store.Options{})
	require.Error(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, ingestionID+".db")))
	openStore(t, dir)
}

// beginWith begins the Ingestion id on the Store.
func beginWith(t *testing.T, db *store.DB, id string) ingest.Status {
	t.Helper()

	return write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Begin(ctx, ingest.Begin{ID: id, SourceURL: sourceURL, At: t0})
	})
}

// watchStatus reads the Store's Status from several goroutines until stop is
// called, and stop returns the Ingestion ids they saw.
func watchStatus(t *testing.T, db *store.DB) (stop func() []string) {
	t.Helper()

	var (
		done atomic.Bool
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make(map[string]bool)
	)

	for range 8 {
		wg.Go(func() {
			for !done.Load() {
				st, err := db.Status(context.Background())
				assert.NoError(t, err)

				mu.Lock()
				seen[st.ID] = true
				mu.Unlock()
			}
		})
	}

	return func() []string {
		done.Store(true)
		wg.Wait()

		return slices.Collect(maps.Keys(seen))
	}
}

// fullStore opens a Store in dir with one committed chunk, whose file can't
// grow past the pages it has, as on a full disk. It returns the Status of
// that commit.
func fullStore(t *testing.T, dir string) (*store.DB, ingest.Status) {
	t.Helper()

	db := openStore(t, dir)
	beginWith(t, db, ingestionID)
	last := commit(t, db, 100, mailFile(1, "a@enron.com"))
	require.NoError(t, store.CapPages(db))

	return db, last
}

// newMailFiles is n Mail files whose Messages no commit has had yet.
func newMailFiles(n int) []message.Identity {
	files := make([]message.Identity, n)
	for i := range files {
		files[i] = mailFile(uint64(i+2), "b@enron.com")
	}

	return files
}

// finishDiskFull records the Ingestion as failed with disk_full.
func finishDiskFull(t *testing.T, db *store.DB) ingest.Status {
	t.Helper()

	return write(t, db, func(ctx context.Context) (ingest.Status, error) {
		return db.Finish(ctx, ingest.Finish{
			State: ingest.StateFailed,
			Error: ingest.Failure{
				Code:    ingest.CodeDiskFull,
				Message: "disk full",
			},
			At: t0.Add(time.Minute),
		})
	})
}

// reserve is the disk reserve of the tests that inject the free space.
const reserve = 64 * 1024 * 1024

// withFreeSpace opens a Store in dir that sees free bytes free, against
// reserve, and begins an Ingestion.
func withFreeSpace(t *testing.T, dir string, free int64) *store.DB {
	t.Helper()

	db := openStore(t, dir, store.Options{
		DiskReserve: reserve,
		FreeSpace:   func(string) (int64, error) { return free, nil },
	})
	beginWith(t, db, ingestionID)

	return db
}

// holdWriteLock takes the Ingestion's write lock on a second connection
// until the test ends.
func holdWriteLock(t *testing.T, dir string) {
	t.Helper()

	ctx := context.Background()

	tx, err := sideConnection(t, dir).BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tx.Rollback()) })

	_, err = tx.ExecContext(ctx, `PRAGMA user_version = 1`)
	require.NoError(t, err)
}

// beginAndClose leaves the Ingestion id's file in dir, with one commit, and
// returns its last Status.
func beginAndClose(t *testing.T, dir, id string) ingest.Status {
	t.Helper()

	db, err := store.Open(dir, store.Options{})
	require.NoError(t, err)

	_, err = db.Begin(
		context.Background(),
		ingest.Begin{ID: id, SourceURL: sourceURL, At: t0},
	)
	require.NoError(t, err)

	last, err := db.Commit(
		context.Background(),
		chunk(100, mailFile(1, "a@enron.com")),
	)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	return last
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()

	data, err := os.ReadFile(from)
	require.NoError(t, err)
	//nolint:gosec // Both paths come from t.TempDir.
	require.NoError(t, os.WriteFile(to, data, 0o600))
}
