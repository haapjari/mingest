package server

import (
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric/noop"
)

// pathStatus is the path the router and the trace filter both name.
const pathStatus = "/status"

// publicRouter routes the API. otelhttp wraps it outermost,
// so a request's span is open while it's routed, observed and handled.
// Each path takes one method; any other path gets the 404 problem.
func (s *Server) publicRouter() http.Handler {
	mux := http.NewServeMux()
	s.route(mux, http.MethodPost, "/start", s.start)
	s.route(mux, http.MethodGet, pathStatus, s.status)
	s.route(mux, http.MethodGet, "/top-senders", s.topSenders)
	mux.HandleFunc("/", s.notFound)

	return otelhttp.NewHandler(s.observe(s.recoverer(mux)), "mingest",
		otelhttp.WithFilter(traced),
		otelhttp.WithMeterProvider(noop.NewMeterProvider()))
}

// route serves h on path for method. Any other method gets the 405 problem
// with Allow, which RFC 9110 requires. The path is registered without its
// method, so ServeMux doesn't answer HEAD or other methods itself, with a
// body that isn't a problem.
func (s *Server) route(
	mux *http.ServeMux,
	method, path string,
	h http.HandlerFunc,
) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			s.writeProblem(w, r, statusProblem(http.StatusMethodNotAllowed,
				r.Method+" isn't allowed on "+r.URL.Path+"."))

			return
		}

		h(w, r)
	})
}

// adminRouter serves the metrics and the profiler. It has no spans and no
// request metrics. A panic is the http.Server's to recover.
func (s *Server) adminRouter() http.Handler {
	mux := http.NewServeMux()

	if s.o.Registry != nil {
		mux.Handle("GET /metrics",
			promhttp.HandlerFor(s.o.Registry, promhttp.HandlerOpts{}))
	}

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

// traced reports whether a request gets a span. A stream doesn't: its span
// would last as long as it's open.
func traced(r *http.Request) bool {
	return r.Method != http.MethodGet || r.URL.Path != pathStatus ||
		negotiate(r.Header.Values("Accept")) != asStream
}
