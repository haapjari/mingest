// Package telemetry sets up tracing and metrics: the OpenTelemetry tracer
// provider, which is always installed so every log line has a trace ID but
// exports only when an endpoint is set, and the Prometheus registry the other
// packages register their instruments in.
package telemetry
