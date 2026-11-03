package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"mingest/pkg/ingest"
)

// Why a stream closed, for its stream closed line.
const (
	reasonTerminal = "terminal"
	reasonClient   = "client"
	reasonShutdown = "shutdown"
	reasonError    = "error"
)

// errShutdown ends a stream's context when the server shuts down.
var errShutdown = errors.New("server shutting down")

// stream handles GET /status when it negotiates to an event stream. A
// client reconnecting after the last event gets 204 without taking a slot.
// Past the cap, it gets 503.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	changed := s.ing.Changed()
	st, err := s.ing.Status(r.Context())

	switch {
	case err != nil:
		s.serverError(w, r, err, detailStatus)
	case ended(st) && r.Header.Get("Last-Event-ID") == eventID(st):
		// The client reconnects with the id of the last event, after
		// which there is nothing more to send.
		w.WriteHeader(http.StatusNoContent)
	case !s.admit():
		s.metrics.rejected.Inc()
		w.Header().Set("Retry-After", retryAfter)
		s.writeProblem(w, r, problem{
			Type:   typeTooManyStreams,
			Title:  "Too many streams",
			Status: http.StatusServiceUnavailable,
			Detail: "The server has as many status streams open as it allows. " +
				"Retry later, or poll GET /status as JSON.",
		})
	default:
		s.open(w, r, st, changed)
	}
}

// admit takes a stream slot, unless all of them are taken.
func (s *Server) admit() bool {
	for {
		n := s.streams.Load()
		if n >= int64(s.o.StreamCap) {
			return false
		}

		if s.streams.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// open serves an admitted stream until it closes, then frees its slot. The
// stream's context also ends when the server shuts down.
func (s *Server) open(
	w http.ResponseWriter,
	r *http.Request,
	st ingest.Status,
	changed <-chan struct{},
) {
	defer s.streams.Add(-1)

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)

	stop := context.AfterFunc(s.closing, func() { cancel(errShutdown) })
	defer stop()

	s.o.Logger.InfoContext(ctx, "stream opened")

	e := &eventStream{
		s:       s,
		ctx:     ctx,
		w:       w,
		rc:      http.NewResponseController(w),
		st:      st,
		changed: changed,
		lastID:  r.Header.Get("Last-Event-ID"),
	}
	reason := e.serve()

	s.o.Logger.InfoContext(ctx, "stream closed",
		slog.String("reason", reason), slog.Int("events", e.events))
}

// eventStream is one open stream: what it has sent, and the snapshot and
// Changed channel it holds.
type eventStream struct {
	s   *Server
	ctx context.Context
	w   http.ResponseWriter
	rc  *http.ResponseController

	st      ingest.Status
	changed <-chan struct{}

	// lastID is the id of the last event sent, or the client's
	// Last-Event-ID before the first.
	lastID string
	events int
}

// serve writes the stream until it closes, and returns why it closed. It
// waits for a change, a ping or the end of the stream: the server shutting
// down, or the client leaving.
func (e *eventStream) serve() string {
	reason := e.begin()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for reason == "" {
		select {
		case <-e.changed:
			reason = e.refresh()
		case <-ping.C:
			if e.write([]byte(": ping\n\n")) != nil {
				reason = reasonClient
			}
		case <-e.ctx.Done():
			reason = reasonClient
			if errors.Is(context.Cause(e.ctx), errShutdown) {
				reason = reasonShutdown
			}
		}
	}

	return reason
}

// begin sets the headers, which the first write sends with 200, then
// writes retry: 3000 and the first snapshot. The read deadline is cleared:
// the server's ReadTimeout would otherwise end the stream after 10 s.
func (e *eventStream) begin() string {
	h := e.w.Header()
	h.Set("Content-Type", mediaStream)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")

	_ = e.rc.SetReadDeadline(time.Time{})

	if e.write([]byte("retry: 3000\n\n")) != nil {
		return reasonClient
	}

	return e.publish()
}

// refresh takes the new Changed channel, then reads the Status, so no
// write between the two is missed.
func (e *eventStream) refresh() string {
	e.changed = e.s.ing.Changed()

	st, err := e.s.ing.Status(e.ctx)
	if err != nil {
		e.s.o.Logger.ErrorContext(e.ctx, "stream status failed",
			slog.Any("error", err))

		return reasonError
	}

	e.st = st

	return e.publish()
}

// publish sends the snapshot if the client hasn't seen it, and closes the
// stream after the last event. While idle there is no id, so only the
// first event is sent.
func (e *eventStream) publish() string {
	id := eventID(e.st)
	unseen := id != e.lastID || (id == "" && e.events == 0)

	if unseen && e.send() != nil {
		return reasonClient
	}

	if ended(e.st) {
		return reasonTerminal
	}

	return ""
}

// send writes the snapshot as a status event.
func (e *eventStream) send() error {
	data, err := json.Marshal(toWire(e.st))
	if err != nil {
		return err
	}

	id := eventID(e.st)

	var b bytes.Buffer
	b.WriteString("event: status\n")

	if id != "" {
		b.WriteString("id: " + id + "\n")
	}

	b.WriteString("data: " + string(data) + "\n\n")

	if err := e.write(b.Bytes()); err != nil {
		return err
	}

	e.lastID, e.events = id, e.events+1

	return nil
}

// write sends b at once. Each write gets its own deadline, so a client
// that stops reading is dropped and frees its slot. Only the write's error
// counts: not every wrapper reports a failed flush.
func (e *eventStream) write(b []byte) error {
	_ = e.rc.SetWriteDeadline(time.Now().Add(writeTimeout))

	_, err := e.w.Write(b)
	_ = e.rc.Flush()

	return err
}

// eventID is <ingestion_id>:<revision>, or empty while idle.
func eventID(st ingest.Status) string {
	if st.State == ingest.StateIdle {
		return ""
	}

	return st.ID + ":" + strconv.FormatInt(st.Revision, 10)
}

// ended reports whether the Ingestion is done or failed.
func ended(st ingest.Status) bool {
	return st.State == ingest.StateDone || st.State == ingest.StateFailed
}
