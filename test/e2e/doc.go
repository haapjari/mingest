//go:build e2e

// Package e2e is the black-box suite. It drives the built binary over HTTP
// and checks the counts, every documented response, the status stream and
// resuming after a crash.
package e2e
