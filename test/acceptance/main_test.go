//go:build acceptance

package acceptance_test

import (
	"fmt"
	"os"
	"testing"

	"mingest/test/harness"
)

// bin is the binary under test, and contract the OpenAPI document every
// response is held to. TestMain sets both.
var (
	bin      string
	contract *harness.Contract
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	var (
		cleanup func()
		err     error
	)

	bin, contract, cleanup, err = harness.Setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	defer cleanup()

	return m.Run()
}
