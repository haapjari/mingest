package server

import (
	"encoding/json"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// The problem types of our own. Every other problem is about:blank, titled
// with its status phrase.
const (
	typeAboutBlank       = "about:blank"
	typeIngestionRunning = "tag:mingest,2026:ingestion-running"
	typeTooManyStreams   = "tag:mingest,2026:too-many-streams"
)

// problem is an RFC 9457 problem detail.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`

	// TraceID names the occurrence: the cause is logged on a line with
	// the same trace_id. It's absent on a stream, which isn't traced.
	TraceID string `json:"trace_id,omitempty"`

	// Ingestion is the running Ingestion a 409 refused to replace.
	Ingestion *ingestionStatus `json:"ingestion,omitempty"`
}

// statusProblem is an about:blank problem for status.
func statusProblem(status int, detail string) problem {
	return problem{
		Type:   typeAboutBlank,
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// writeProblem writes the problem as application/problem+json, with the
// request's trace id when it's traced.
func (s *Server) writeProblem(
	w http.ResponseWriter,
	r *http.Request,
	p problem,
) {
	if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
		p.TraceID = sc.TraceID().String()
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)

	s.record(r, json.NewEncoder(w).Encode(p))
}

// notFound handles any path no route matches, with the 404 problem.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.writeProblem(w, r, statusProblem(http.StatusNotFound,
		"There is no resource at "+r.URL.Path+"."))
}
