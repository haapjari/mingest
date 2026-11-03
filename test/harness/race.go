//go:build race

package harness

// raceFlags builds the binary with the race detector when the tests run
// under it.
var raceFlags = []string{"-race"}
