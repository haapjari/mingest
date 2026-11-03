package server

import "strings"

// The media types GET /status serves.
const (
	mediaJSON   = "application/json"
	mediaStream = "text/event-stream"
)

// representation is what a GET /status negotiated to.
type representation int

const (
	notAcceptable representation = iota
	asJSON
	asStream
)

// negotiate picks the representation for the Accept header values. JSON
// wins whenever the client accepts it, and no Accept means JSON. The event
// stream is served only to a client that asks for it and not for JSON.
// Parameters such as q are ignored.
func negotiate(accept []string) representation {
	if len(accept) == 0 {
		return asJSON
	}

	json, stream := false, false

	for part := range strings.SplitSeq(strings.Join(accept, ","), ",") {
		media, _, _ := strings.Cut(part, ";")

		switch strings.ToLower(strings.TrimSpace(media)) {
		case mediaJSON, "application/*", "*/*":
			json = true
		case mediaStream, "text/*":
			stream = true
		}
	}

	switch {
	case json:
		return asJSON
	case stream:
		return asStream
	}

	return notAcceptable
}
