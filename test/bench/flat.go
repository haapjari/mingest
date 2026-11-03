package bench

import (
	"errors"
	"fmt"
)

// maxGrowthPercent is how far peak RSS at a larger scale may exceed the
// base's.
const maxGrowthPercent = 10

// Flat fails if the peak RSS of any run in larger exceeds base's by more
// than 10%. The base is 10×: 1× is a warm-up, before SQLite's page cache
// and the Go heap reach their working size.
func Flat(base Result, larger []Result) error {
	limit := base.PeakRSS * (100 + maxGrowthPercent)

	var errs []error
	for _, r := range larger {
		if r.PeakRSS*100 > limit {
			errs = append(errs, fmt.Errorf(
				"peak RSS at %d× is %s, over %d× (%s) by more than 10%%",
				r.Scale, mib(r.PeakRSS), base.Scale, mib(base.PeakRSS)))
		}
	}

	return errors.Join(errs...)
}
