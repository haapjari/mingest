package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// unmatched is the route label of a request no route matched.
const unmatched = "unmatched"

// errKey holds a *error in a request's context: why the request failed,
// for its request line.
type errKey struct{}

// record keeps err as the request's error, for its request line. A request
// observe didn't see has nowhere to keep it.
func (s *Server) record(r *http.Request, err error) {
	if p, ok := r.Context().Value(errKey{}).(*error); ok {
		*p = errors.Join(*p, err)
	}
}

// recoverer turns a panic in a handler into a 500 problem, and records the
// panic and its stack as the request's error, where http.Server would
// close the connection without an answer. http.ErrAbortHandler panics on, so the server aborts the
// connection as the handler meant.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}

			if err, ok := v.(error); ok &&
				errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}

			err := fmt.Errorf("panic: %v\n%s", v, debug.Stack())

			// Once the response has started, a problem would be appended to
			// it, so the panic is logged and the connection aborted.
			if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
				s.o.Logger.ErrorContext(r.Context(), "handler panicked after "+
					"answering", slog.Any("error", err))
				panic(http.ErrAbortHandler)
			}

			s.serverError(w, r, err, "The server failed to handle the request.")
		}()

		next.ServeHTTP(w, r)
	})
}

// observe logs one line per request. ServeMux sets r.Pattern on the
// request it's given, so the route is known once the handler returns.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w}

		var err error
		r = r.WithContext(context.WithValue(r.Context(), errKey{}, &err))

		next.ServeHTTP(ww, r)

		s.finish(r, ww, err, time.Since(start))
	})
}

// statusWriter records the status and the bytes a handler wrote. Unwrap
// lets http.ResponseController reach the connection's Flush and deadlines,
// which the event stream needs.
type statusWriter struct {
	http.ResponseWriter

	status, bytes int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(b)
	w.bytes += n

	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// finish logs a request that has been handled. The request
// line is info, or error for a 5xx with the error the handler recorded. At
// debug it adds who asked and how much was sent. A handler that wrote no
// status sent 200.
func (s *Server) finish(
	r *http.Request,
	ww *statusWriter,
	err error,
	d time.Duration,
) {
	route := routeOf(r)

	status := ww.status
	if status == 0 {
		status = http.StatusOK
	}

	// The span is named after the route, as the OTel HTTP conventions ask,
	// once ServeMux has matched one.
	if route != unmatched {
		span := trace.SpanFromContext(r.Context())
		span.SetName(r.Method + " " + route)
		span.SetAttributes(attribute.String("http.route", route))
	}

	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("route", route),
		slog.Int("status", status),
		slog.Float64("duration_seconds", d.Seconds()),
	}

	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}

	if s.o.Logger.Enabled(r.Context(), slog.LevelDebug) {
		attrs = append(attrs,
			slog.String("remote_addr", r.RemoteAddr),
			slog.String("user_agent", r.UserAgent()),
			slog.String("accept", r.Header.Get("Accept")),
			slog.Int("bytes", ww.bytes))
	}

	level := slog.LevelInfo
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
	}

	s.o.Logger.LogAttrs(r.Context(), level, "request", attrs...)
}

// routeOf is the matched route pattern, or unmatched for the catch-all
// that answers 404.
func routeOf(r *http.Request) string {
	if r.Pattern == "" || r.Pattern == "/" {
		return unmatched
	}

	return r.Pattern
}
