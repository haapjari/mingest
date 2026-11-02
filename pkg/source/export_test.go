package source

// NewIdleReader and ErrIdle expose the idle reader to the source_test
// package, which tests it under synctest with an in-memory reader.
var (
	NewIdleReader = newIdleReader
	ErrIdle       = errIdle
)
