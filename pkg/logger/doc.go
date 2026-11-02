// Package logger builds the process's JSON logger. It is the only package
// that knows which slog handler is behind it, and it adds the trace ID from
// the context to every line.
package logger
