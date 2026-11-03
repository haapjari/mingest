package store

import (
	"github.com/prometheus/client_golang/prometheus"
)

// metrics are the Store's Prometheus collectors.
type metrics struct {
	commitDuration prometheus.Histogram
	storeBytes     prometheus.GaugeFunc
}

// newMetrics makes the collectors. size reports the current file's bytes
// at each scrape.
func newMetrics(size func() int64) *metrics {
	return &metrics{
		commitDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "mingest_commit_duration_seconds",
			Help: "How long one chunk's commit took, from its free-space check to COMMIT.",
		}),
		storeBytes: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "mingest_store_bytes",
			Help: "The current Ingestion's database file and its WAL, in bytes.",
		}, func() float64 { return float64(size()) }),
	}
}

// register adds both collectors to r. A nil r is a no-op.
func (m *metrics) register(r prometheus.Registerer) error {
	if r == nil {
		return nil
	}

	if err := r.Register(m.commitDuration); err != nil {
		return err
	}

	return r.Register(m.storeBytes)
}
