package server_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"

	"mingest/pkg/ingest"
	"mingest/pkg/server"
)

// spans records every span the tests end.
var spans = tracetest.NewSpanRecorder()

// TestMain installs a real tracer provider, as run does, so requests get
// trace ids, and records the spans.
func TestMain(m *testing.M) {
	otel.SetTracerProvider(
		sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
	)
	os.Exit(m.Run())
}

// Not parallel: goleak sees every goroutine in the process.
func TestServer_Run_ShutsDownWithinTimeout(t *testing.T) {
	running := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, running) })

	ts := newTestServer(t)
	ts.run(t)()

	assert.Empty(t, ts.logs.find(t, "shutdown step timed out"))
}

func TestServer_Listen_FailsOnAddressInUse(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	taken, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = taken.Close() }()

	srv, err := server.New(&fakeIngester{}, server.Options{
		ListenAddress:      taken.Addr().String(),
		AdminListenAddress: "127.0.0.1:0",
	})
	require.NoError(t, err)

	assert.ErrorContains(t, srv.Listen(t.Context()), "address already in use")
}

func TestServer_Run_FailsWhenAListenerBreaks(t *testing.T) {
	t.Parallel()

	for _, which := range []string{"public", "admin"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()

			ts := newTestServer(t)
			require.NoError(t, ts.srv.Listen(t.Context()))

			public, admin := ts.srv.Listeners()
			broken := map[string]net.Listener{"public": public, "admin": admin}
			require.NoError(t, broken[which].Close())

			assert.ErrorContains(t, ts.srv.Run(t.Context()), "serve")
		})
	}
}

func TestServer_Handler_ListsRouteMethodsInAllow(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"DELETE /status":    http.MethodDelete,
		"HEAD /top-senders": http.MethodHead,
	}

	for name, method := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := strings.Fields(name)[1]
			resp := do(t, newTestServer(t).handler, method, path)

			require.Equal(t, http.StatusMethodNotAllowed, resp.Code)
			assert.Equal(t, "GET", resp.Header().Get("Allow"))

			if method != http.MethodHead {
				p := decodeProblem(t, resp)
				assert.Equal(t, "Method Not Allowed", p["title"])
				assert.Regexp(t, `^[0-9a-f]{32}$`, p["trace_id"])
			}
		})
	}
}

// testServer is a Server over a fake Ingester, with its log and metrics.
type testServer struct {
	srv     *server.Server
	handler http.Handler
	ing     *fakeIngester
	logs    *logBuffer
	reg     *prometheus.Registry
}

func newTestServer(t testing.TB) *testServer {
	t.Helper()

	return newTestServerAt(t, slog.LevelInfo)
}

func newTestServerAt(t testing.TB, level slog.Level) *testServer {
	t.Helper()

	return newTestServerOf(t, level, 10)
}

func newTestServerOf(
	t testing.TB,
	level slog.Level,
	streamCap int,
) *testServer {
	t.Helper()

	ts := &testServer{
		ing:  &fakeIngester{status: ingest.Status{State: ingest.StateIdle}},
		logs: &logBuffer{},
		reg:  prometheus.NewRegistry(),
	}

	srv, err := server.New(ts.ing, server.Options{
		ListenAddress:      "127.0.0.1:0",
		AdminListenAddress: "127.0.0.1:0",
		StreamCap:          streamCap,
		Logger:             ts.logs.logger(level),
		Registry:           ts.reg,
	})
	require.NoError(t, err)

	ts.srv, ts.handler = srv, srv.Handler()

	return ts
}

// run listens and serves in the background. The func it returns stops the
// server and waits for Run to return without an error.
func (ts *testServer) run(t *testing.T) func() {
	t.Helper()

	require.NoError(t, ts.srv.Listen(t.Context()))

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)

	go func() { errs <- ts.srv.Run(ctx) }()

	return func() {
		cancel()
		require.NoError(t, <-errs)
	}
}
