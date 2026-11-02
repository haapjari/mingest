# The pinned tools are in hack/make/tools.mk.

# --- Make  -------------------------------------------------------------------

# Strict mode in every recipe, a failing command, or a failing first command
# of a pipe, fails the target.
SHELL := /usr/bin/env bash -euo pipefail

# A recipe that fails leaves no half-written file behind, so the next run
# makes it again.
.DELETE_ON_ERROR:

# V=1 shows the commands too.
V ?=
ifneq ($(V),1)
.SILENT:
endif

.DEFAULT_GOAL := build

# --- Toolchain ---------------------------------------------------------------

# Every target runs exactly the Go version in go.mod's go line (go 1.27.3
# becomes go1.27.3), not just that version or newer: the go command
# downloads it if the local one differs.
GO_VERSION := $(shell awk '$$1 == "go" { print $$2 }' go.mod)
export GOTOOLCHAIN := go$(GO_VERSION)

# Absolute path, so a recipe finds its tools from any directory.
TOOLS_DIR := $(CURDIR)/_output/tools

# sha256sum is GNU's; macOS ships shasum.
SHA256SUM := $(shell command -v sha256sum || echo shasum -a 256)

include hack/make/tools.mk

# --- Settings ----------------------------------------------------------------

IMAGE ?= mingest:dev
BENCH_BASE ?= 50000
FUZZ_TIME ?= 30s

# The real dataset for test-acceptance, its sha256 is checked after download.
DATASET_URL := https://www.cs.cmu.edu/~enron/enron_mail_20150507.tar.gz
DATASET_SOURCE ?= $(DATASET_URL)
DATASET_SHA256 := b3da1b3fe0369ec3140bb4fbce94702c33b7da810ec15d718b3fadf5cd748ca7
DATASET := _output/dataset/enron_mail_20150507.tar.gz

# --- Build -------------------------------------------------------------------

.PHONY: build docker run

build:
	echo '>> building _output/bin/mingest'
	CGO_ENABLED=0 go build -trimpath -o _output/bin/mingest ./cmd/mingest

docker:
	echo '>> building the image $(IMAGE)'
	docker build -t $(IMAGE) .

run: build
	echo '>> running _output/bin/mingest'
	mkdir -p _output/data
	MINGEST_DATASET_SOURCE='$(DATASET_SOURCE)' MINGEST_DATA_DIR=_output/data \
		_output/bin/mingest

# --- Install Pinned Tools ----------------------------------------------------

.PHONY: tools
tools: $(TOOLS)

# --- Formatters and Linters Configured in .golangci.yml-----------------------

.PHONY: check fmt go-version govulncheck links lint openapi secrets spelling unused vet

# pre-commit targets
check: secrets lint vet unused spelling go-version

fmt: $(GOLANGCI_LINT)
	echo '>> formatting'
	$(GOLANGCI_LINT) fmt

lint: $(GOLANGCI_LINT)
	echo '>> linting'
	$(GOLANGCI_LINT) run ./...

vet:
	echo '>> vetting code'
	go vet -tags e2e,container,bench,acceptance ./...
	GOOS=darwin go vet -tags e2e,container,bench,acceptance ./...

unused:
	echo '>> checking go.mod for unused or missing modules'
	go mod tidy -diff || { echo 'Run go mod tidy.' >&2; exit 1; }

go-version:
	echo '>> checking the dockerfile builds with go $(GO_VERSION)'
	grep -qx 'ARG BUILD_IMAGE=golang:$(GO_VERSION)' Dockerfile \
		|| { echo 'set ARG BUILD_IMAGE=golang:$(GO_VERSION) in the dockerfile' >&2; exit 1; }

openapi: $(VACUUM)
	echo '>> linting docs/openapi.yaml'
	$(VACUUM) lint --no-banner --errors --details docs/openapi.yaml

govulncheck: $(GOVULNCHECK)
	echo '>> checking for known vulnerabilities'
	$(GOVULNCHECK) ./...

# The Markdown a commit would hold, copied out so a gitignored file counts as
# missing. Docker mounts the copy, so it lives under _output, not $TMPDIR.
links:
	echo '>> checking the links in the Markdown'
	rm -rf _output/links && mkdir -p _output/links
	git ls-files -z --cached --others --exclude-standard \
		| xargs -0 tar -cf - | tar -xf - -C _output/links
	docker run --rm -v '$(CURDIR)/_output/links:/input:ro,z' -w /input \
		$(LYCHEE_IMAGE) --offline --include-fragments --no-progress '**/*.md'

# QUICK=1 scans only staged changes (pre-commit), else every commit.
secrets: $(GITLEAKS)
	echo '>> scanning for secrets'
	$(GITLEAKS) git --no-banner --redact $(if $(QUICK),--staged --pre-commit)

# testdata/ holds real and fixture mail, misspellings included.
spelling: $(MISSPELL)
	echo '>> checking spelling'
	git ls-files -z ':!:**/testdata/**' | xargs -0 $(MISSPELL) -error

# --- Test --------------------------------------------------------------------

.PHONY: test test-coverage test-e2e test-container test-fuzz test-acceptance \
	bench dataset

# QUICK=1 keeps go's test cache, so only packages a change affects rerun.
test: $(GOTESTSUM)
	echo '>> running unit tests'
	$(GOTESTSUM) -- -race $(if $(QUICK),,-count=1) ./...

# The profile is _output/coverage.out, and
# go tool cover -html=_output/coverage.out shows it line by line.
test-coverage: $(GOTESTSUM)
	echo '>> running unit tests with coverage'
	mkdir -p _output
	$(GOTESTSUM) -- -race -count=1 -covermode=atomic \
		-coverprofile=_output/coverage.out ./...
	go tool cover -func=_output/coverage.out | tail -n 1

test-e2e: $(GOTESTSUM)
	echo '>> running e2e tests'
	$(GOTESTSUM) -- -race -count=1 -tags e2e ./test/e2e/...

test-container: docker $(GOTESTSUM)
	echo '>> running container tests against $(IMAGE)'
	TEST_IMAGE=$(IMAGE) \
		$(GOTESTSUM) -- -count=1 -tags container ./test/container/...

test-fuzz:
	echo '>> fuzzing each target for $(FUZZ_TIME)'
	for pkg in $$(go list ./...); do \
		for target in $$(go test -list '^Fuzz' $$pkg | grep '^Fuzz'); do \
			go test -run '^$$' -fuzz "^$$target$$" -fuzztime $(FUZZ_TIME) $$pkg || exit 1; \
		done; \
	done

test-acceptance: $(GOTESTSUM) $(DATASET)
	echo '>> running acceptance tests'
	TEST_DATASET=$(CURDIR)/$(DATASET) \
		$(GOTESTSUM) -- -count=1 -tags acceptance ./test/acceptance/...

bench: $(GOTESTSUM)
	echo '>> running benchmarks'
	TEST_BENCH_BASE=$(BENCH_BASE) \
		$(GOTESTSUM) -- -count=1 -tags bench ./test/bench/...

dataset: $(DATASET)

$(DATASET):
	echo '>> downloading the dataset'
	mkdir -p $(@D)
	curl --fail --silent --show-error --location --retry 3 \
		--output $@.part '$(DATASET_URL)'
	echo '$(DATASET_SHA256)  $@.part' | $(SHA256SUM) -c - \
		|| { rm -f $@.part; exit 1; }
	mv $@.part $@

# --- CI ----------------------------------------------------------------------

.PHONY: ci hooks

# The pre-push hook runs it.
# Pre-commit runs make check test QUICK=1.
ci: check openapi links govulncheck test test-e2e test-fuzz docker \
	test-container

hooks: $(LEFTHOOK)
	echo '>> installing the git hooks'
	$(LEFTHOOK) install

# --- Clean -------------------------------------------------------------------

.PHONY: clean
clean:
	echo '>> removing _output/'
	rm -rf _output
