package store

import (
	"context"
	"fmt"
)

// CapPages stops the Store's file from growing past the pages it has, as a
// full disk would.
func CapPages(d *DB) error {
	ctx := context.Background()

	var pages int64
	if err := d.writer.QueryRowContext(ctx, `PRAGMA page_count`).
		Scan(&pages); err != nil {
		return err
	}

	_, err := d.writer.ExecContext(
		ctx,
		fmt.Sprintf(`PRAGMA max_page_count = %d`, pages),
	)

	return err
}

// CacheSize returns the writer connection's cache_size.
func CacheSize(d *DB) (int64, error) {
	var size int64
	err := d.writer.QueryRowContext(context.Background(), `PRAGMA cache_size`).
		Scan(&size)

	return size, err
}
