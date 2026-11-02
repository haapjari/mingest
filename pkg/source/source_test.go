package source_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"mingest/pkg/source"
)

func TestFromURL_RejectsOtherURLs(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"http":                "http://example.com/enron.tar.gz",
		"ftp":                 "ftp://example.com/enron.tar.gz",
		"no scheme":           "/data/enron.tar.gz",
		"a file on a host":    "file://example.com/enron.tar.gz",
		"a file with no path": "file://",
		"https with no host":  "https:///enron.tar.gz",
		"garbage":             "://\x7f",
	}
	for name, rawURL := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src, err := source.FromURL(rawURL, source.Options{})
			require.Error(t, err)
			assert.Nil(t, src)
		})
	}
}

func TestFileSource_Open_ChangesVersionWithMtime(t *testing.T) {
	t.Parallel()

	path := writeFile(t, "dataset")
	before := open(t, "file://"+path).Version

	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(path, later, later))

	assert.NotEqual(t, before, open(t, "file://"+path).Version)
}

func TestHTTPSource_Open_ReportsUnknownSizeWithoutContentLength(t *testing.T) {
	t.Parallel()

	stream := openServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Flushing before the body forces chunked encoding.
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "dataset")
	})

	assert.Equal(t, int64(-1), stream.Size)
	assert.Equal(t, "dataset", readAll(t, stream))
}

func TestHTTPSource_Open_TakesVersionFromHeaders(t *testing.T) {
	t.Parallel()

	const lastModified = "Wed, 21 Oct 2015 07:28:00 GMT"

	tests := map[string]struct {
		etag, lastModified string
		want               string
	}{
		"a strong ETag": {
			etag:         `"v1"`,
			lastModified: lastModified,
			want:         `"v1"`,
		},
		"a weak ETag gives way to Last-Modified": {
			etag:         `W/"v1"`,
			lastModified: lastModified,
			want:         lastModified,
		},
		"Last-Modified alone": {
			lastModified: lastModified,
			want:         lastModified,
		},
		"a weak ETag alone": {
			etag: `W/"v1"`,
		},
		"neither": {},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stream := openServer(
				t,
				func(w http.ResponseWriter, _ *http.Request) {
					setHeader(w, "ETag", tt.etag)
					setHeader(w, "Last-Modified", tt.lastModified)
				},
			)

			assert.Equal(t, tt.want, stream.Version)
		})
	}
}

func TestHTTPSource_Open_FailsOnAnyStatusButOK(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusPartialContent,
		http.StatusNotFound,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			src := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})

			_, err := src.Open(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprint(status))
		})
	}
}

func TestHTTPSource_Open_FailsWhenBodyIsCut(t *testing.T) {
	t.Parallel()

	stream := openServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "only part of it")
		_ = http.NewResponseController(w).Flush()

		panic(http.ErrAbortHandler)
	})

	_, err := io.ReadAll(stream)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.NotErrorIs(t, err, source.ErrIdle)
}

func TestHTTPSource_Open_DoesNotDecompress(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer

	zw := gzip.NewWriter(&compressed)
	_, err := io.WriteString(zw, "dataset")
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	stream := openServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	})

	assert.Equal(t, compressed.String(), readAll(t, stream))
}

func TestHTTPSource_Open_FailsWhenBodyStalls(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "first bytes")
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
		},
	))
	t.Cleanup(server.Close)

	stream := openWith(t, server.URL, source.Options{
		Client:      server.Client(),
		IdleTimeout: 50 * time.Millisecond,
	})

	// The timer itself is tested under synctest in idle_test.go. Here, a
	// body that stops sending ends in ErrIdle, however much of it arrived
	// first on a loaded machine.
	_, err := io.ReadAll(stream)
	assert.ErrorIs(t, err, source.ErrIdle)
}

func TestHTTPSource_Open_VerifiesCertificatesByDefault(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	src, err := source.FromURL(server.URL, source.Options{})
	require.NoError(t, err)

	_, err = src.Open(context.Background())

	var unknown x509.UnknownAuthorityError
	require.ErrorAs(t, err, &unknown)
}

func TestHTTPSource_Open_StopsWithContext(t *testing.T) {
	t.Parallel()

	src := serve(t, func(_ http.ResponseWriter, _ *http.Request) {})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Open(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

// TestFromURL_LeaksNothingFromStreams ends a Stream each way it can end, and checks
// that nothing it started outlives it. It isn't parallel, since goleak
// sees the whole process.
func TestFromURL_LeaksNothingFromStreams(t *testing.T) {
	server := newLeakServer(t)

	tests := map[string]func(t *testing.T){
		"read to the end": func(t *testing.T) {
			readAll(t, server.open(t, "/", neverIdle))
		},
		"closed unread": func(t *testing.T) {
			server.open(t, "/", neverIdle)
		},
		"stalled": func(t *testing.T) {
			_, err := io.ReadAll(server.open(t, "/stall", stallTimeout))
			assert.ErrorIs(t, err, source.ErrIdle)
		},
		"not found": func(t *testing.T) {
			_, err := server.source(t, "/missing").Open(context.Background())
			assert.Error(t, err)
		},
		"a file": func(t *testing.T) {
			readAll(t, open(t, "file://"+writeFile(t, "dataset")))
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			checkLeaks(t)
			run(t)
		})
	}
}

// writeFile writes data to a new file and returns its path.
func writeFile(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "enron.tar.gz")
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	return path
}

// open opens the Source at rawURL with the default Options, and closes the
// Stream when the test ends.
func open(t *testing.T, rawURL string) *source.Stream {
	t.Helper()

	return openWith(t, rawURL, source.Options{IdleTimeout: neverIdle})
}

func openWith(t *testing.T, rawURL string, opts source.Options) *source.Stream {
	t.Helper()

	src, err := source.FromURL(rawURL, opts)
	require.NoError(t, err)

	stream, err := src.Open(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stream.Close()) })

	return stream
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()

	data, err := io.ReadAll(r)
	require.NoError(t, err)

	return string(data)
}

// serve starts a TLS server with handler and returns the HTTP Source of
// its URL, using the server's client.
func serve(t *testing.T, handler http.HandlerFunc) source.Source {
	t.Helper()

	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	src, err := source.FromURL(
		server.URL+"/enron.tar.gz",
		source.Options{Client: server.Client(), IdleTimeout: neverIdle},
	)
	require.NoError(t, err)

	return src
}

// openServer opens the HTTP Source of a server with handler, and closes the
// Stream when the test ends.
func openServer(t *testing.T, handler http.HandlerFunc) *source.Stream {
	t.Helper()

	stream, err := serve(t, handler).Open(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stream.Close()) })

	return stream
}

func setHeader(w http.ResponseWriter, key, value string) {
	if value != "" {
		w.Header().Set(key, value)
	}
}

// checkLeaks fails the test if a goroutine it starts is still running when
// it ends. Call it first, so its check runs after every other cleanup.
func checkLeaks(t *testing.T) {
	t.Helper()

	running := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, running) })
}

// The leak test's idle timeouts. The idle timer would release a Stream
// that Close failed to, so it waits an hour except where a stall is the
// point.
const (
	neverIdle    = time.Hour
	stallTimeout = 50 * time.Millisecond
)

// leakServer is a TLS server for the leak test, with a client that keeps
// no connection alive, so a closed body leaves nothing running.
type leakServer struct {
	url    string
	client *http.Client
}

func newLeakServer(t *testing.T) *leakServer {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(leakRoutes))
	t.Cleanup(server.Close)

	transport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)

	transport = transport.Clone()
	transport.DisableKeepAlives = true

	return &leakServer{
		url:    server.URL,
		client: &http.Client{Transport: transport},
	}
}

// leakRoutes answers /missing with 404, stalls /stall after the headers,
// and serves the dataset on any other path.
func leakRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/missing":
		w.WriteHeader(http.StatusNotFound)
	case "/stall":
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	default:
		_, _ = io.WriteString(w, "dataset")
	}
}

func (s *leakServer) source(t *testing.T, path string) source.Source {
	t.Helper()

	src, err := source.FromURL(s.url+path, s.options(neverIdle))
	require.NoError(t, err)

	return src
}

// open opens path with idle as the IdleTimeout, and closes the Stream when
// the test ends.
func (s *leakServer) open(
	t *testing.T,
	path string,
	idle time.Duration,
) *source.Stream {
	t.Helper()

	return openWith(t, s.url+path, s.options(idle))
}

func (s *leakServer) options(idle time.Duration) source.Options {
	return source.Options{Client: s.client, IdleTimeout: idle}
}
