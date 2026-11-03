package bench

import (
	"fmt"
	"time"
)

// Result is one run of the binary over a synthetic Dataset.
type Result struct {
	// Scale is the Dataset's size as a multiple of the base: 1, 10 or 100.
	Scale int

	// MailFiles is the Dataset's, as the generator planned.
	MailFiles int

	// Duration is from POST /start to done.
	Duration time.Duration

	// PeakRSS is the process's peak resident set, from getrusage.
	PeakRSS int64
}

func mib(n int64) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
