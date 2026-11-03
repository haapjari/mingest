# Operations

## API

The contract is [openapi.yaml](openapi.yaml). Errors are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem details with a `trace_id` that the server's log line for the cause also carries.

| Method | Path           | Port | Description                                                                     |
| ------ | -------------- | ---- | ------------------------------------------------------------------------------- |
| `POST` | `/start`       | 8080 | Start an ingestion. Returns `202`, or `409` if one is already running.          |
| `GET`  | `/status`      | 8080 | Ingestion status as JSON, or as an SSE stream with `Accept: text/event-stream`. |
| `GET`  | `/top-senders` | 8080 | The ten senders with the most messages.                                         |
| `GET`  | `/metrics`     | 8081 | Prometheus metrics: `mingest_*`, the Go runtime and the process.                |
| `GET`  | `/debug/pprof/` | 8081 | Go profiler.                                                                   |

## Configuration

All configuration comes from environment variables. Unknown `MINGEST_*` variables are rejected at startup.

| Variable                       | Default     | Description                                                               |
| ------------------------------ | ----------- | ------------------------------------------------------------------------- |
| `MINGEST_DATASET_SOURCE`       | required    | Dataset URL (`https://` or `file://`).                                    |
| `MINGEST_DATA_DIR`             | required    | Directory for the SQLite store.                                           |
| `MINGEST_LISTEN_ADDRESS`       | `:8080`     | Public API address.                                                       |
| `MINGEST_ADMIN_LISTEN_ADDRESS` | `:8081`     | Admin address (metrics, profiler).                                        |
| `MINGEST_LOG_LEVEL`            | `info`      | `debug`, `info`, `warn` or `error`.                                       |
| `MINGEST_SSE_MAX_STREAMS`      | `100`       | Maximum open SSE streams.                                                 |
| `GOMEMLIMIT`                   | unset       | Go's soft memory limit. Set it to about 90% of the container's limit.     |
| `OTEL_EXPORTER_OTLP_ENDPOINT`  | unset       | OTLP endpoint for traces. Tracing is off when unset.                      |

Compose sets `GOMEMLIMIT` next to the container's memory limit, and SQLite's heap is a fixed 16 MiB outside it. A free-space check before each commit fails the ingestion with `disk_full`.

## Deployment

```sh
docker compose up --build --wait
```

Starts mingest with Jaeger, Prometheus and Grafana, all bound to `127.0.0.1`. `--wait` waits for the image's `HEALTHCHECK`, which runs `mingest healthcheck`: a `GET /status` that must answer `200`.

## Operations

- **Shutdown and resume**: on `SIGTERM` the HTTP servers drain for up to 5 s while the ingester stops, and the last spans are exported within 5 s. A running ingestion stays `running` and resumes from its last committed chunk when the process next starts. So does an ingestion whose end couldn't be recorded after 3 tries; until then, `POST /start` answers `409`. A lost connection to the source resumes the same way, with 5 retries and backoff, before the ingestion fails with `source_unreachable`.
- **Logs**: JSON lines to stdout through `log/slog`. A line written while handling a traced request carries its `trace_id`.
- **Security**: a distroless image running as non-root with a read-only root filesystem, `:8081` bound to `127.0.0.1` in compose, and plain HTTP behind a TLS-terminating proxy. Server-Sent Events responses send `X-Accel-Buffering: no`, so a proxy such as nginx doesn't hold them back.
- **One replica**: a lock on the data directory stops a second process.
