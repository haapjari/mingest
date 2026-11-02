package source

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ErrScheme is a Dataset URL whose scheme isn't file or https.
var ErrScheme = errors.New(
	"source: the Dataset URL must be file:// or https://",
)

// Options configures the HTTP Source. IdleTimeout must be set.
type Options struct {
	// Client sends the request. Nil means a client with dial,
	// TLS-handshake and response-header timeouts, and no overall one,
	// since a download can take as long as it takes.
	Client *http.Client

	// IdleTimeout is the longest wait for the next byte of the body.
	IdleTimeout time.Duration
}

// FromURL returns the Source for rawURL, chosen by its scheme: file:// for
// a local file, https:// for a download.
func FromURL(rawURL string, opts Options) (Source, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("source: parse the Dataset URL: %w", err)
	}

	switch u.Scheme {
	case "file":
		return newFile(rawURL, u)
	case "https":
		return newHTTP(rawURL, u, opts)
	}

	return nil, fmt.Errorf("%w: %q", ErrScheme, rawURL)
}
