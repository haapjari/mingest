package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"mingest/pkg/ingest"
)

// start handles POST /start. It begins an Ingestion. Start gets a context
// the client can't cancel, so hanging up can't leave a begun Ingestion
// behind. The body isn't read.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	st, err := s.ing.Start(context.WithoutCancel(r.Context()))

	switch {
	case errors.Is(err, ingest.ErrRunning):
		// The 409 holds the running Ingestion's Status.
		running := toWire(st)
		s.writeProblem(w, r, problem{
			Type:      typeIngestionRunning,
			Title:     "Ingestion running",
			Status:    http.StatusConflict,
			Detail:    "An ingestion is already running. Follow it at /status.",
			Ingestion: &running,
		})
	case err != nil:
		s.serverError(w, r, err, "The server failed to start the ingestion.")
	default:
		w.Header().Set("Location", "/status")
		s.writeJSON(w, r, http.StatusAccepted, toWire(st))
	}
}

// detailStatus is the 500's detail when the Status can't be read.
const detailStatus = "The server failed to read the ingestion status."

// status handles GET /status. It serves the Status as JSON or as an event
// stream, or 406. Every answer varies by Accept.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Accept")

	switch negotiate(r.Header.Values("Accept")) {
	case asJSON:
		st, err := s.ing.Status(r.Context())
		if err != nil {
			s.serverError(w, r, err, detailStatus)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		s.writeJSON(w, r, http.StatusOK, toWire(st))
	case asStream:
		s.stream(w, r)
	default:
		s.writeProblem(w, r, statusProblem(http.StatusNotAcceptable,
			"GET /status serves "+mediaJSON+" and "+mediaStream+"."))
	}
}

// topSenders handles GET /top-senders. It serves the Top Senders as of the
// last commit.
func (s *Server) topSenders(w http.ResponseWriter, r *http.Request) {
	ts, err := s.ing.TopSenders(r.Context())
	if err != nil {
		s.serverError(w, r, err, "The server failed to read the top senders.")

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, topSendersToWire(ts))
}

// serverError writes a 500 with a generic detail, and keeps err for the
// request line: the client sees none of it.
func (s *Server) serverError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	detail string,
) {
	s.record(r, err)
	s.writeProblem(w, r, statusProblem(http.StatusInternalServerError, detail))
}

// writeJSON writes v as the JSON body of a status response.
func (s *Server) writeJSON(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	v any,
) {
	w.Header().Set("Content-Type", mediaJSON)
	w.WriteHeader(status)

	// A failed write is the request's error; record ignores nil.
	s.record(r, json.NewEncoder(w).Encode(v))
}
