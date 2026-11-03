//go:build container

package container_test

import (
	"fmt"
	"os"
	"testing"

	"mingest/test/harness"
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	image = os.Getenv("TEST_IMAGE")
	if image == "" {
		fmt.Fprintln(os.Stderr, "TEST_IMAGE is unset: run make test-container")

		return 1
	}

	var err error

	contract, err = harness.OpenAPI()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return m.Run()
}

// image is the image under test, and contract the OpenAPI document its
// responses are held to. TestMain sets both.
var (
	image    string
	contract *harness.Contract
)
