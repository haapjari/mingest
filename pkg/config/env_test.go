package config_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/pkg/config"
)

func TestLoad_NamesBothMissingRequiredVariables(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"unset": nil,
		"empty": {"MINGEST_DATASET_SOURCE=", "MINGEST_DATA_DIR="},
	}

	for name, environ := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(environ)
			require.Error(t, err)

			assert.ErrorContains(t, err, "MINGEST_DATASET_SOURCE")
			assert.ErrorContains(t, err, "MINGEST_DATA_DIR")
		})
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(required())
	require.NoError(t, err)

	assert.Equal(t, "file:///dataset.tar.gz", cfg.DatasetSource)
	assert.Equal(t, "/data", cfg.DataDir)
	assert.Equal(t, ":8080", cfg.ListenAddress)
	assert.Equal(t, ":8081", cfg.AdminListenAddress)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
	assert.Equal(t, 100, cfg.SSEMaxStreams)
}

func TestLoad_ChecksListenAddresses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		environ []string
		wantErr string
	}{
		"public without a port": {
			required("MINGEST_LISTEN_ADDRESS=localhost"),
			"MINGEST_LISTEN_ADDRESS",
		},
		"admin without a port": {
			required("MINGEST_ADMIN_LISTEN_ADDRESS=localhost"),
			"MINGEST_ADMIN_LISTEN_ADDRESS",
		},
		"port 0": {required(
			"MINGEST_LISTEN_ADDRESS=127.0.0.1:0",
			"MINGEST_ADMIN_LISTEN_ADDRESS=127.0.0.1:0",
		), ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(tt.environ)

			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func TestLoad_RejectsStreamCapBelowOne(t *testing.T) {
	t.Parallel()

	_, err := config.Load(required("MINGEST_SSE_MAX_STREAMS=0"))

	assert.ErrorContains(t, err, "MINGEST_SSE_MAX_STREAMS")
}

func TestLoad_RejectsUnknownMingestVariable(t *testing.T) {
	t.Parallel()

	_, err := config.Load(required("MINGEST_LISTEN_ADDR=:9090"))

	assert.ErrorContains(t, err, "unknown variable MINGEST_LISTEN_ADDR")
}

func TestListenAddress_ReadsOnlyItsVariable(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		environ []string
		want    string
	}{
		"default": {nil, ":8080"},
		"set": {
			[]string{"MINGEST_LISTEN_ADDRESS=127.0.0.1:9090"},
			"127.0.0.1:9090",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := config.ListenAddress(tt.environ)
			require.NoError(t, err)

			assert.Equal(t, tt.want, got)
		})
	}
}

// required returns an environ with both required variables, then extra.
func required(extra ...string) []string {
	return append([]string{
		"MINGEST_DATASET_SOURCE=file:///dataset.tar.gz",
		"MINGEST_DATA_DIR=/data",
	}, extra...)
}
