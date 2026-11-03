// Mingest ingests a dataset of mail files, counts its messages and senders,
// and serves the ingestion status and the top senders over HTTP.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"mingest/pkg/config"
	"mingest/pkg/ingest"
	"mingest/pkg/logger"
	"mingest/pkg/server"
	"mingest/pkg/source"
	"mingest/pkg/store"
	"mingest/pkg/telemetry"
)

// telemetryTimeout bounds exporting the last spans at shutdown.
const telemetryTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer stop()

	// The first SIGINT or SIGTERM starts a graceful shutdown. Calling stop
	// then restores the default behavior, so a second Ctrl+C kills the
	// process at once.
	context.AfterFunc(ctx, stop)

	os.Exit(dispatch(ctx, os.Args[1:]))
}

// dispatch runs the command the arguments name, and returns the exit code:
// none runs the service, "healthcheck" asks it for /status. Anything else
// prints the usage and exits with 2, the flag package's convention.
func dispatch(ctx context.Context, args []string) int {
	var err error

	switch {
	case len(args) == 0:
		err = run(ctx, os.Environ(), os.Stdout)
	case len(args) == 1 && args[0] == "healthcheck":
		err = healthcheck(ctx, os.Environ())
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "mingest healthcheck:", err)
		}
	default:
		_, _ = fmt.Fprintln(os.Stderr, "usage: mingest [healthcheck]")
		return 2
	}

	if err != nil {
		return 1
	}

	return 0
}

// run is the service. It opens every dependency in order, each with a
// deferred close, serves until ctx ends, and so shuts down in reverse. It
// logs its own outcome as its last line: startup failed, or stopped.
func run(ctx context.Context, environ []string, stdout io.Writer) (err error) {
	cfg, err := config.Load(environ)

	// A config that failed to load leaves the level at its zero, info.
	log := logger.New(logger.Options{Writer: stdout, Level: cfg.LogLevel})

	listening := false

	defer func() {
		switch {
		case err != nil && !listening:
			log.ErrorContext(ctx, "startup failed", slog.Any("error", err))
		case err != nil:
			log.ErrorContext(ctx, "stopped", slog.Any("error", err))
		default:
			log.InfoContext(ctx, "stopped")
		}
	}()

	if err != nil {
		return err
	}

	log.InfoContext(ctx, "starting",
		slog.Int("gomaxprocs", runtime.GOMAXPROCS(0)),
		slog.Int64("gomemlimit_bytes", debug.SetMemoryLimit(-1)),
		slog.Int64("sqlite_heap_limit_bytes", cfg.Pipeline.SQLiteHeapLimit),
		slog.Int("sse_stream_cap", cfg.SSEMaxStreams),
		slog.String("dataset_source", redacted(cfg.DatasetSource)),
		slog.String("data_dir", cfg.DataDir))

	tel, err := telemetry.New(ctx, telemetry.Options{
		Endpoint: cfg.OTLPEndpoint,
		Logger:   log,
	})
	if err != nil {
		return err
	}

	// The last spans, the shutdown's own included, are exported within
	// telemetryTimeout.
	defer func() {
		sctx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), telemetryTimeout)
		defer cancel()

		err = errors.Join(err, tel.Shutdown(sctx))
	}()

	src, err := source.FromURL(cfg.DatasetSource, source.Options{
		IdleTimeout: cfg.Pipeline.IdleTimeout,
	})
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DataDir, store.Options{
		DiskReserve: cfg.Pipeline.DiskReserve,
		HeapLimit:   cfg.Pipeline.SQLiteHeapLimit,
		Registerer:  tel.Registry(),
	})
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, db.Close()) }()

	ing, err := ingest.New(db, src, ingest.Options{
		ChunkMailFiles:  cfg.Pipeline.ChunkMailFiles,
		ChunkBytes:      cfg.Pipeline.ChunkBytes,
		ChunkAge:        cfg.Pipeline.ChunkAge,
		ReadBuffer:      cfg.Pipeline.ReadBuffer,
		ChannelCapacity: cfg.Pipeline.ChannelCapacity,
		SourceRetries:   cfg.Pipeline.SourceRetries,
		RetryBase:       cfg.Pipeline.RetryBase,
		RetryMax:        cfg.Pipeline.RetryMax,
		SourceURL:       cfg.DatasetSource,
		Logger:          log,
		Registerer:      tel.Registry(),
	})
	if err != nil {
		return err
	}

	srv, err := server.New(ing, server.Options{
		ListenAddress:      cfg.ListenAddress,
		AdminListenAddress: cfg.AdminListenAddress,
		StreamCap:          cfg.SSEMaxStreams,
		Logger:             log,
		Registry:           tel.Registry(),
	})
	if err != nil {
		return err
	}

	if err := srv.Listen(ctx); err != nil {
		return err
	}

	listening = true

	// The Ingester and the server run until ctx ends or either fails,
	// which stops the other. An Ingestion cut short stays running and
	// resumes at the next start.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return ing.Run(gctx) })
	g.Go(func() error { return srv.Run(gctx) })
	g.Go(func() error {
		<-gctx.Done()
		log.InfoContext(ctx, "shutting down",
			slog.String("cause", context.Cause(gctx).Error()))

		return nil
	})

	return g.Wait()
}

// redacted hides a password in rawURL, which can carry credentials.
func redacted(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "(unparsable)"
	}

	return u.Redacted()
}
