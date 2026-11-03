package ingest

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// metrics holds the Ingester's Prometheus collectors.
type metrics struct {
	stageDuration *prometheus.HistogramVec
	bufferChunks  prometheus.Gauge

	mailFiles prometheus.Counter

	sourceRetries prometheus.Counter
	readBytes     prometheus.Gauge
	sourceBytes   prometheus.Gauge
	startedAt     prometheus.Gauge
	finishedAt    prometheus.Gauge
}

func newMetrics() *metrics {
	m := &metrics{
		stageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mingest_stage_duration_seconds",
			Help: "How long one chunk took in a Pipeline stage.",
		}, []string{"stage"}),
		bufferChunks: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_buffer_chunks",
			Help: "The parsed chunks waiting for Write.",
		}),
		mailFiles: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mingest_mail_files_total",
			Help: "The Mail files committed.",
		}),
		sourceRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mingest_source_retries_total",
			Help: "The attempts made again after a Source error.",
		}),
		readBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_ingestion_read_bytes",
			Help: "The current Ingestion's compressed bytes read, as committed.",
		}),
		sourceBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_ingestion_source_bytes",
			Help: "The current Ingestion's Source size, or 0 while unknown.",
		}),
		startedAt: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_ingestion_started_timestamp_seconds",
			Help: "When the current Ingestion started, or 0 while idle.",
		}),
		finishedAt: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mingest_ingestion_finished_timestamp_seconds",
			Help: "When the current Ingestion finished, or 0 until it does.",
		}),
	}

	return m
}

// register adds every collector to r. A nil r is a no-op.
func (m *metrics) register(r prometheus.Registerer) error {
	if r == nil {
		return nil
	}

	for _, c := range m.collectors() {
		if err := r.Register(c); err != nil {
			return err
		}
	}

	return nil
}

func (m *metrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.stageDuration, m.bufferChunks, m.mailFiles,
		m.sourceRetries, m.readBytes,
		m.sourceBytes, m.startedAt, m.finishedAt,
	}
}

// progress sets the progress gauges from a Status: the bytes, and when the
// Ingestion started and finished.
func (m *metrics) progress(st Status) {
	m.readBytes.Set(float64(st.BytesRead))
	m.sourceBytes.Set(float64(max(st.BytesTotal, 0)))
	m.startedAt.Set(unixSeconds(st.StartedAt))
	m.finishedAt.Set(unixSeconds(st.FinishedAt))
}

// unixSeconds is t as Unix seconds, or 0 when t isn't set.
func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}

	return float64(t.UnixNano()) / float64(time.Second)
}

// stage records the time since start as one chunk's duration in stage.
func (m *metrics) stage(stage string, start time.Time) {
	m.stageDuration.WithLabelValues(stage).Observe(time.Since(start).Seconds())
}
