# Contributing

Requirements: Go (the version in `go.mod`, fetched automatically), Docker, GNU Make. Linux or macOS; on macOS the end-to-end tests that need an HTTPS source are skipped, because Go there ignores `SSL_CERT_FILE`. Other tools are pinned and installed into `_output/tools` on demand.

```sh
make            # build _output/bin/mingest
make run        # run locally against the CMU dataset
make hooks      # install git hooks (pre-commit: check + test, pre-push: ci)
```

| Target                 | Description                                                       |
| ---------------------- | ----------------------------------------------------------------- |
| `make check`           | Lint, vet, unused code, spelling, secrets, the Go version.        |
| `make fmt`             | Format the Go code, as `make check` requires.                     |
| `make test`            | Unit tests. Add `QUICK=1` to keep the test cache.                 |
| `make test-coverage`   | Unit tests with coverage, profile in `_output/coverage.out`.      |
| `make test-e2e`        | End-to-end tests.                                                 |
| `make test-fuzz`       | Fuzz tests (`FUZZ_TIME`, default `30s`).                          |
| `make test-container`  | Tests against the built image.                                    |
| `make test-acceptance` | Full run against the real dataset (downloaded, sha256-checked).   |
| `make bench`           | Memory benchmark at 1×, 10× and 100× (`BENCH_BASE`, about 3 min). |
| `make links`           | Check the links in the Markdown, offline.                         |
| `make docker`          | Build the image (`IMAGE`, default `mingest:dev`).                 |
| `make dataset`         | Download the Enron dataset for `make test-acceptance`.            |
| `make tools`           | Install the pinned tools into `_output/tools`.                    |
| `make openapi`         | Lint the OpenAPI contract.                                        |
| `make ci`              | Every check and test suite; the pre-push hook runs it.            |
| `make clean`           | Remove `_output`.                                                 |

`V=1` prints the commands each target runs.

## Tests

| Level | Proves |
| --- | --- |
| Unit | The message rules, chunking, resume and the state machine. |
| Fuzz | No bad mail file or archive can crash the service or break a bound. |
| Black-box | The built binary keeps the API contract and resumes after `SIGKILL`. |
| Container | The image fails an ingestion cleanly when its data volume runs low on space. |
| Benchmark | Memory stays flat from 10× to 100×. |
| Acceptance | The real Enron dataset gives the expected counts and top senders. |
