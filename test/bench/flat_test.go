package bench_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mingest/test/bench"
)

const mb = 1 << 20

func TestFlat_PassesWithinTenPercent(t *testing.T) {
	t.Parallel()

	err := bench.Flat(bench.Result{Scale: 10, PeakRSS: 100 * mb},
		[]bench.Result{{Scale: 100, PeakRSS: 105 * mb}})

	require.NoError(t, err)
}

func TestFlat_PassesAtExactlyTenPercent(t *testing.T) {
	t.Parallel()

	err := bench.Flat(bench.Result{Scale: 10, PeakRSS: 1000},
		[]bench.Result{{Scale: 100, PeakRSS: 1100}})

	require.NoError(t, err)
}

func TestFlat_FailsOverTenPercent(t *testing.T) {
	t.Parallel()

	err := bench.Flat(bench.Result{Scale: 10, PeakRSS: 100 * mb},
		[]bench.Result{{Scale: 100, PeakRSS: 111 * mb}})

	require.Error(t, err)
	assert.Equal(t,
		"peak RSS at 100× is 111.0 MiB, over 10× (100.0 MiB) "+
			"by more than 10%", err.Error())
}

func TestFlat_ReportsEveryScaleOver(t *testing.T) {
	t.Parallel()

	err := bench.Flat(bench.Result{Scale: 1, PeakRSS: 100 * mb},
		[]bench.Result{
			{Scale: 10, PeakRSS: 200 * mb},
			{Scale: 100, PeakRSS: 300 * mb},
		})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "at 10×")
	assert.Contains(t, err.Error(), "at 100×")
}

func TestFlat_PassesWithNothingToCompare(t *testing.T) {
	t.Parallel()

	require.NoError(t, bench.Flat(bench.Result{Scale: 10}, nil))
}
