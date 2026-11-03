//go:build e2e

package e2e_test

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"testing"

	"mingest/test/harness"
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

	return checked(m.Run())
}

// checked runs the contract-coverage check after a whole, passing run. A run
// filtered by -run or -skip, or on macOS, where the HTTPS tests are skipped,
// leaves responses unseen by design.
func checked(code int) int {
	if code != 0 || filtered() || runtime.GOOS == "darwin" {
		return code
	}

	if err := contract.Check(excluded); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return 0
}

func filtered() bool {
	return flag.Lookup("test.run").Value.String() != "" ||
		flag.Lookup("test.skip").Value.String() != ""
}

// bin is the binary under test, and contract the OpenAPI document every
// response is held to. TestMain sets both.
var (
	bin      string
	contract *harness.Contract
)

const problem = "application/problem+json"

// r is a documented response.
func r(operation string, status int, contentType string) harness.Response {
	return harness.Response{
		Operation: operation, Status: status, ContentType: contentType,
	}
}

// excluded are the documented responses the suite can't cause from outside.
var excluded = map[harness.Response]string{
	r("startIngestion", 500, problem): "a server error; can't be caused from outside",
	r("getStatus", 500, problem):      "a server error; can't be caused from outside",
	r("getTopSenders", 500, problem):  "a server error; can't be caused from outside",
}
