package server

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics holds the server's Prometheus collectors.
type metrics struct {
	streams    prometheus.GaugeFunc
	streamsMax prometheus.Gauge
	rejected   prometheus.Counter
}

// newMetrics builds the collectors. The open-streams gauge reads streams,
// and the cap's gauge is set to streamCap once.
func newMetrics(streams *atomic.Int64, streamCap int) *metrics {
	m := &metrics{
		streams: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "mingest_sse_streams",
			Help: "The event streams open on GET /status.",
		}, func() float64 { return float64(streams.Load()) }),
		streamsMax: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_sse_streams_max",
			Help: "The cap on event streams open at once.",
		}),
		rejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mingest_sse_streams_rejected_total",
			Help: "The event streams refused because the cap was reached.",
		}),
	}
	m.streamsMax.Set(float64(streamCap))

	return m
}

// register adds every collector to r.
func (m *metrics) register(r prometheus.Registerer) error {
	for _, c := range m.collectors() {
		if err := r.Register(c); err != nil {
			return err
		}
	}

	return nil
}

func (m *metrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.streams, m.streamsMax, m.rejected,
	}
}
