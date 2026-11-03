// Package bench measures memory: peak RSS stays flat as the dataset grows.
//
// The suite runs only with -tags bench, through make bench. Without the tag
// the package holds the suite's logic and its unit tests: the source that
// streams a synthetic Dataset and the flatness gate.
package bench
