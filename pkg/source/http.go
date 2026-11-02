package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The default client's timeouts. A download has no overall timeout: it
// takes as long as it takes, and the idle reader catches a stall.
const (
	dialTimeout           = 30 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
)

var (
	// errHTTPURL is an https:// URL with no host.
	errHTTPURL = errors.New("source: an https:// URL needs a host")

	// errStatus is a response other than 200 OK.
	errStatus = errors.New("source: unexpected status")
)

// httpSource is the Source of a download.
type httpSource struct {
	url  string
	u    *url.URL
	opts Options
}

func newHTTP(rawURL string, u *url.URL, opts Options) (*httpSource, error) {
	if u.Host == "" {
		return nil, fmt.Errorf("%w: %q", errHTTPURL, rawURL)
	}

	if opts.Client == nil {
		opts.Client = defaultClient()
	}

	return &httpSource{url: rawURL, u: u, opts: opts}, nil
}

// defaultClient has dial, TLS-handshake and response-header timeouts, and
// no overall timeout.
func defaultClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ForceAttemptHTTP2:     true,
	}}
}

// Open sends the GET. The Stream's reads fail once ctx is done, or once
// IdleTimeout passes with no byte. The Source never reconnects: a failed
// read is the caller's to retry.
func (h *httpSource) Open(ctx context.Context) (*Stream, error) {
	ctx, cancel := context.WithCancel(ctx)

	// The URL is already parsed, so the request is built, not parsed
	// again. Asking for the identity encoding stops the transport from
	// decompressing a gzip Content-Encoding, so the bytes and the size
	// stay the archive's.
	req := (&http.Request{
		Method: http.MethodGet,
		URL:    h.u,
		Header: http.Header{"Accept-Encoding": {"identity"}},
	}).WithContext(ctx)

	resp, err := h.opts.Client.Do(req)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("source: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		cancel()

		return nil, errors.Join(
			fmt.Errorf("%w: GET %s: %s", errStatus, h.url, resp.Status),
			resp.Body.Close(),
		)
	}

	// The Stream's body closes the response body.
	return &Stream{
		ReadCloser: &body{
			idleReader: newIdleReader(resp.Body, h.opts.IdleTimeout, cancel),
			closer:     resp.Body,
			cancel:     cancel,
		},
		URL:     h.url,
		Size:    resp.ContentLength,
		Version: version(resp.Header),
	}, nil
}

// version is the strong ETag, else Last-Modified, else "". A weak ETag
// doesn't promise the same bytes, so it can't tell a resume that the
// Dataset is unchanged.
func version(h http.Header) string {
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		return etag
	}

	return h.Get("Last-Modified")
}

// body is a response body read through the idle reader. Closing it stops
// the timer and releases the request.
type body struct {
	*idleReader

	closer io.Closer
	cancel context.CancelFunc
}

func (b *body) Close() error {
	b.Stop()
	defer b.cancel()

	return b.closer.Close()
}
