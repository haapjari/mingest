//go:build e2e

package e2e_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/harness"
)

func TestHealthcheck_PassesOnReadyService(t *testing.T) {
	t.Parallel()

	r := healthcheck(t, hostPort(t, start(t).URL))

	assert.Equal(t, 0, r.Code, r.Output)
}

func TestHealthcheck_FailsOnStoppedService(t *testing.T) {
	t.Parallel()

	p := start(t)
	addr := hostPort(t, p.URL)
	p.Stop(t)

	checking := time.Now()
	r := healthcheck(t, addr)

	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Output, "mingest healthcheck:")
	assert.Less(t, time.Since(checking), 3*time.Second)
}

func TestHealthcheck_RefusesPortZero(t *testing.T) {
	t.Parallel()

	r := healthcheck(t, ":0")

	assert.Equal(t, 1, r.Code)
	assert.Contains(t, r.Output, "port 0")
}

func TestHealthcheck_ChecksUnspecifiedHostOnLoopback(t *testing.T) {
	t.Parallel()

	_, port, err := net.SplitHostPort(hostPort(t, start(t).URL))
	require.NoError(t, err)

	r := healthcheck(t, "0.0.0.0:"+port)

	assert.Equal(t, 0, r.Code, r.Output)
}

// healthcheck runs mingest healthcheck against addr, with no other
// variable of its own.
func healthcheck(t *testing.T, addr string) harness.Result {
	t.Helper()

	return harness.Run(t, bin, []string{"healthcheck"},
		"MINGEST_LISTEN_ADDRESS="+addr)
}

// hostPort is the host:port of an http:// URL.
func hostPort(t *testing.T, url string) string {
	t.Helper()

	addr, ok := strings.CutPrefix(url, "http://")
	require.True(t, ok, url)

	return addr
}
