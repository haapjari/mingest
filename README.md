# mingest

mingest ingests a mail dataset and reports its progress and top senders.

## Quick Start

```sh
docker compose up --build --wait
curl -X POST localhost:8080/start
curl -N -H 'Accept: text/event-stream' localhost:8080/status
curl localhost:8080/top-senders
```

| Service    | URL                      |
| ---------- | ------------------------ |
| API        | <http://localhost:8080>  |
| Admin      | <http://localhost:8081>  |
| Grafana    | <http://localhost:3001>  |
| Prometheus | <http://localhost:9090>  |
| Jaeger     | <http://localhost:16686> |

## Documentation

- [docs/design.md](docs/design.md)
- [docs/operations.md](docs/operations.md)
- [docs/openapi.yaml](docs/openapi.yaml)
- [CONTRIBUTING.md](CONTRIBUTING.md)
