//go:build bench

package bench_test

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"testing"

	"mingest/test/harness"
)

// fileDataset reads the Dataset over file:// instead of HTTPS, as on macOS,
// where Go ignores SSL_CERT_FILE. It's forced on Linux with -args -file, so
// the macOS path runs without a Mac.
var fileDataset = flag.Bool("file", runtime.GOOS == "darwin",
	"read the Dataset over file:// instead of HTTPS")

// bin is the binary under test. TestMain sets it.
var bin string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	var (
		cleanup func()
		err     error
	)

	bin, _, cleanup, err = harness.Setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	defer cleanup()

	return m.Run()
}
