package config

import "time"

// The Pipeline's numbers.
const (
	// chunkMailFiles is the most Mail files in one chunk.
	chunkMailFiles = 1000

	// chunkBytes is the most bytes of Mail files in one chunk.
	chunkBytes = 4 * 1024 * 1024

	// chunkAge is how long a chunk stays open after its first Mail file.
	chunkAge = time.Second

	// channelCapacity is how many chunks the channel from Read to Write
	// holds.
	channelCapacity = 2

	// readBuffer is the buffer between the Source and gunzip.
	readBuffer = 1024 * 1024

	// diskReserve is the free space a commit must leave on the disk.
	diskReserve = 64 * 1024 * 1024

	// sourceRetries is how many times a failed Source is retried.
	sourceRetries = 5

	// retryBase is the first backoff between Source retries.
	retryBase = time.Second

	// retryMax is the longest backoff between Source retries.
	retryMax = 30 * time.Second

	// idleTimeout ends an HTTP Source that sends nothing for this long.
	idleTimeout = 60 * time.Second

	// sqliteHeapLimit is SQLite's hard heap limit, outside the Go heap.
	// The page cache takes at most 2 MiB of it; the memory benchmark
	// measured SQLite's RSS at about twice its heap.
	sqliteHeapLimit = 16 * 1024 * 1024
)

// Pipeline holds the Pipeline's numbers. They are constants, not
// variables: an operator has no better value to give.
type Pipeline struct {
	// ChunkMailFiles closes a chunk after this many Mail files.
	ChunkMailFiles int

	// ChunkBytes closes a chunk after this many bytes of Mail files.
	ChunkBytes int64

	// ChunkAge closes a chunk this long after its first Mail file.
	ChunkAge time.Duration

	// ChannelCapacity is how many chunks the channel from Read to Write
	// holds.
	ChannelCapacity int

	// ReadBuffer is the buffer between the Source and gunzip.
	ReadBuffer int

	// DiskReserve is the free space a commit must leave.
	DiskReserve int64

	// SourceRetries is how often a failed Source is retried.
	SourceRetries int

	// RetryBase and RetryMax bound the backoff between retries.
	RetryBase, RetryMax time.Duration

	// IdleTimeout ends an HTTP Source that sends nothing for this long.
	IdleTimeout time.Duration

	// SQLiteHeapLimit is SQLite's hard heap limit in bytes.
	SQLiteHeapLimit int64
}

func pipeline() Pipeline {
	return Pipeline{
		ChunkMailFiles:  chunkMailFiles,
		ChunkBytes:      chunkBytes,
		ChunkAge:        chunkAge,
		ChannelCapacity: channelCapacity,
		ReadBuffer:      readBuffer,
		DiskReserve:     diskReserve,
		SourceRetries:   sourceRetries,
		RetryBase:       retryBase,
		RetryMax:        retryMax,
		IdleTimeout:     idleTimeout,
		SQLiteHeapLimit: sqliteHeapLimit,
	}
}
