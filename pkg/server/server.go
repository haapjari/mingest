package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"

	"mingest/pkg/ingest"
)

// The server's protocol constants. They aren't Pipeline sizing, so they
// live here, not in config.
const (
	// readHeaderTimeout stops slow-header clients.
	readHeaderTimeout = 5 * time.Second

	// readTimeout bounds reading a request. No handler reads a body.
	readTimeout = 10 * time.Second

	// writeTimeout bounds writing a response. A stream pushes it forward
	// before each write.
	writeTimeout = 10 * time.Second

	// idleTimeout bounds a keep-alive connection between requests.
	idleTimeout = 120 * time.Second

	// shutdownTimeout is how long the HTTP shutdown step waits for the
	// requests in flight before it closes the connections.
	shutdownTimeout = 5 * time.Second

	// pingInterval is how often an idle stream sends a comment, so proxies
	// and clients see it's alive.
	pingInterval = 15 * time.Second

	// retryAfter is the Retry-After of a stream refused at the cap, in
	// seconds.
	retryAfter = "5"
)

// Ingester is what the server needs of the Ingester.
type Ingester interface {
	// Start begins an Ingestion. While one runs, it returns that one's
	// Status with ingest.ErrRunning.
	Start(ctx context.Context) (ingest.Status, error)

	// Status returns the Status as of the last write.
	Status(ctx context.Context) (ingest.Status, error)

	// TopSenders returns the Top Senders as of the last commit.
	TopSenders(ctx context.Context) (ingest.TopSenders, error)

	// Changed returns a channel that is closed at the next write.
	Changed() <-chan struct{}
}

// Options configures the Server.
type Options struct {
	// ListenAddress and AdminListenAddress are the public and admin
	// listeners' host:port. Port 0 picks a free one.
	ListenAddress      string
	AdminListenAddress string

	// StreamCap is how many event streams may be open at once.
	StreamCap int

	// Logger gets the request lines and the server's own lines.
	Logger *slog.Logger

	// Registry gets the server's metrics, and the admin port's /metrics
	// serves it. Nil leaves both out.
	Registry *prometheus.Registry
}

// Server serves the API on a public listener and metrics and profiling on
// an admin one.
type Server struct {
	ing     Ingester
	o       Options
	metrics *metrics

	public http.Handler
	admin  http.Handler

	// publicL and adminL are the listeners Listen bound, which Run serves.
	publicL net.Listener
	adminL  net.Listener

	// closing ends when the public server starts shutting down, so open
	// streams end: http.Server.Shutdown doesn't cancel them.
	closing      context.Context
	closeStreams context.CancelFunc

	// streams counts the open event streams, up to Options.StreamCap.
	streams atomic.Int64
}

// New returns a Server in front of the Ingester. It fails only if its
// metrics can't be registered with opts.Registry.
func New(ing Ingester, opts Options) (*Server, error) {
	s := &Server{ing: ing, o: opts}
	// closeStreams runs when the public server shuts down.
	s.closing, s.closeStreams = context.WithCancel( //nolint:gosec // above
		context.Background(),
	)

	s.metrics = newMetrics(&s.streams, opts.StreamCap)
	if opts.Registry != nil {
		if err := s.metrics.register(opts.Registry); err != nil {
			return nil, fmt.Errorf("register metrics: %w", err)
		}
	}

	s.public = s.publicRouter()
	s.admin = s.adminRouter()

	return s, nil
}

// Handler returns the public router, which the unit tests serve with
// httptest.
func (s *Server) Handler() http.Handler {
	return s.public
}

// errNotListening is Run called before Listen.
var errNotListening = errors.New("server: Run before Listen")

// Listen binds both addresses and logs the bound ones. It's a startup step
// of its own, so a port in use fails the startup, not the run.
//
// It opens the public listener, then the admin one, and closes the first if
// the second fails.
func (s *Server) Listen(ctx context.Context) error {
	var lc net.ListenConfig

	public, err := lc.Listen(ctx, "tcp", s.o.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	admin, err := lc.Listen(ctx, "tcp", s.o.AdminListenAddress)
	if err != nil {
		return errors.Join(fmt.Errorf("listen: %w", err), public.Close())
	}

	s.publicL, s.adminL = public, admin
	s.o.Logger.InfoContext(ctx, "listening",
		slog.String("listen_address", public.Addr().String()),
		slog.String("admin_listen_address", admin.Addr().String()))

	return nil
}

// Run serves on the listeners Listen bound until ctx ends. Then open
// streams end, and both servers shut down within shutdownTimeout. It returns once both have stopped.
func (s *Server) Run(ctx context.Context) error {
	if s.publicL == nil {
		return errNotListening
	}

	pub, adm := newHTTPServer(s.public), newHTTPServer(s.admin)
	pub.RegisterOnShutdown(s.closeStreams)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return serve(pub, s.publicL) })
	g.Go(func() error { return serve(adm, s.adminL) })
	g.Go(func() error { <-gctx.Done(); return s.shutdown(ctx, pub, adm) })

	return g.Wait()
}

func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// serve serves on listener until the server shuts down, which isn't an error.
func serve(srv *http.Server, l net.Listener) error {
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// shutdown is the HTTP step of the process's shutdown: streams are told to
// end, and both servers stop, or are closed if they
// haven't within shutdownTimeout.
func (s *Server) shutdown(ctx context.Context, servers ...*http.Server) error {
	sctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), shutdownTimeout,
	)
	defer cancel()

	var errs []error
	for _, srv := range servers {
		errs = append(errs, srv.Shutdown(sctx))
	}

	err := errors.Join(errs...)
	if err == nil {
		return nil
	}

	s.o.Logger.ErrorContext(ctx, "shutdown step timed out",
		slog.String("step", "http"), slog.Duration("timeout", shutdownTimeout))

	for _, srv := range servers {
		err = errors.Join(err, srv.Close())
	}

	return err
}
