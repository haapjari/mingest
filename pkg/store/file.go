package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"modernc.org/sqlite"

	"mingest/pkg/ingest"
)

// pragmas apply to every connection. WAL with synchronous=NORMAL can lose
// the last commits on power loss but never corrupts the file, and
// mmap_size=0 keeps the file out of the process's memory. Transactions
// take the write lock at BEGIN.
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=mmap_size(0)" +
	"&_pragma=busy_timeout(5000)" +
	"&_txlock=immediate"

// fileName is the database file of the Ingestion id.
func fileName(dir, id string) string {
	return filepath.Join(dir, id+".db")
}

// current lists dir's database files: the newest, which is the current
// Ingestion, or "" when there is none, and the older ones. Only files named
// <UUID>.db are the Store's. UUIDv7s sort by time, and the entries come
// sorted by name. The directory is listed, not globbed, so its own name is
// never read as a pattern.
func current(dir string) (string, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("store: %w", err)
	}

	var paths []string

	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".db")
		if _, err := uuid.Parse(id); ok && err == nil && e.Type().IsRegular() {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}

	if len(paths) == 0 {
		return "", nil, nil
	}

	return paths[len(paths)-1], paths[:len(paths)-1], nil
}

// removeFiles deletes each database file with its WAL and shared memory
// file. A file that is already gone is not an error.
func removeFiles(paths ...string) error {
	var errs []error

	for _, path := range paths {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			err := os.Remove(path + suffix)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("store: %w", err))
			}
		}
	}

	return errors.Join(errs...)
}

// fileBytes is the current file's size with its WAL, or 0 with no file.
func (d *DB) fileBytes() int64 {
	path := *d.path.Load()
	if path == "" {
		return 0
	}

	return bytesOf(path) + bytesOf(path+"-wal")
}

func bytesOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	return info.Size()
}

// maxCache caps SQLite's page cache. A larger cache didn't make the
// memory benchmark faster, and it fills as the database grows, so peak RSS
// would grow with the Dataset.
const maxCache = 2 * 1024 * 1024

// connect opens one writer connection to the database file at path. It
// goes through a Connector rather than sql.Open, which can only fail on a
// driver name it doesn't know.
//
// SQLite's heap is capped at heapLimit. Its page cache is maxCache, or 7/8
// of a smaller heapLimit, leaving room for statements.
func connect(path string, heapLimit int64) *sql.DB {
	dsn := fmt.Sprintf(
		"file:%s%s&_pragma=hard_heap_limit(%d)&_pragma=cache_size(%d)",
		path,
		pragmas,
		heapLimit,
		-min(maxCache, heapLimit*7/8)/1024,
	)

	db := sql.OpenDB(connector{dsn: dsn})
	db.SetMaxOpenConns(1)

	return db
}

// connector opens connections to one database through modernc's driver.
type connector struct {
	dsn string
}

func (c connector) Connect(_ context.Context) (driver.Conn, error) {
	return c.Driver().Open(c.dsn)
}

func (connector) Driver() driver.Driver {
	return &sqlite.Driver{}
}

// errUnfinished is a database file without the schema: Begin created it
// but crashed before its transaction committed.
var errUnfinished = errors.New("the file has no schema")

// openFile opens an existing database file. It reads the progress row and
// the top ten in one read transaction, so they match each other. A file
// without the schema fails with errUnfinished.
func openFile(
	ctx context.Context,
	path string,
	heapLimit int64,
) (*sql.DB, ingest.TopSenders, error) {
	db := connect(path, heapLimit)

	top, err := inTx(ctx, db, &sql.TxOptions{ReadOnly: true},
		func(tx *sql.Tx) (ingest.TopSenders, error) {
			var tables int

			err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema "+
				"WHERE type = 'table' AND name = 'ingestion'").
				Scan(&tables)
			if err == nil && tables == 0 {
				err = errUnfinished
			}

			if err != nil {
				return ingest.TopSenders{}, err
			}

			st, err := scanStatus(tx.QueryRowContext(ctx, selectProgress))
			if err != nil {
				return ingest.TopSenders{}, err
			}

			return readTop(ctx, tx, st)
		})
	if err != nil {
		return nil, ingest.TopSenders{}, errors.Join(
			fmt.Errorf("store: read %s: %w", path, err),
			db.Close(),
		)
	}

	return db, top, nil
}
