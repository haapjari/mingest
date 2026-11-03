//go:build race

package dataset_test

// The race detector slows gzip about 20 times, so the heap test streams 100k
// Mail files under it, still 10 times its baseline.
const flatMailFiles = 100_000
