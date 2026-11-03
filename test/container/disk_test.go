//go:build container

package container_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"mingest/test/dataset"
	"mingest/test/harness"
)

// The volume is below the 64 MiB a commit must leave free, so the image's
// free-space check fails the Ingestion before the disk is full.
func TestImage_Start_FailsIngestionOnLowDisk(t *testing.T) {
	t.Parallel()

	fixture := strings.TrimPrefix(
		dataset.WriteFile(t, dataset.Fixture()), "file://")
	url := startMingest(
		t,
		map[string]string{
			"MINGEST_DATASET_SOURCE": "file:///data/fixture.tar.gz",
		},
		func(hc *container.HostConfig) {
			// Docker mounts a tmpfs as root's, so mode 1777 lets the
			// image's user write.
			hc.Tmpfs = map[string]string{
				"/var/lib/mingest": "size=16m,mode=1777",
			}
			hc.Binds = []string{fixture + ":/data/fixture.tar.gz:ro"}
		},
	)

	require.Equal(t, http.StatusAccepted, harness.Send(t, contract.Client(),
		http.MethodPost, url+"/start", nil))

	st := harness.WaitStatus(t, contract.Client(), url, 30*time.Second,
		harness.Status.Ended)

	assert.Equal(t, "failed", st.State)
	require.NotNil(t, st.Error)
	assert.Equal(t, "disk_full", st.Error.Code)
}

// startMingest runs the image with env and the host config changes, waits
// until /status answers, and returns the API's URL. The container is
// removed when the test ends; testcontainers' reaper removes it even if
// the test binary dies.
func startMingest(
	t *testing.T,
	env map[string]string,
	host func(*container.HostConfig),
) string {
	t.Helper()

	c, err := testcontainers.GenericContainer(t.Context(),
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:              image,
				Env:                env,
				ExposedPorts:       []string{"8080/tcp"},
				HostConfigModifier: host,
				WaitingFor: wait.ForHTTP("/status").WithPort("8080/tcp").
					WithStartupTimeout(30 * time.Second),
			},
			Started: true,
		})
	testcontainers.CleanupContainer(t, c)
	require.NoError(t, err)

	url, err := c.PortEndpoint(t.Context(), "8080/tcp", "http")
	require.NoError(t, err)

	return url
}
