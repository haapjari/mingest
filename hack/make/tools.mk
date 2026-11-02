# Pinned development tools. Included from the top-level Makefile, which sets
# TOOLS_DIR.
#
# Go tools are built with go install, which checks every module against Go's
# checksum database and builds for this machine, Linux or macOS. Each goes
# into its own <name>-<version>/ directory, so bumping a version builds it
# afresh. Tools not written in Go run from their official images, pinned by
# digest.


# --- Pins --------------------------------------------------------------------

GOLANGCI_LINT_VERSION := v2.15.0
GOTESTSUM_VERSION     := v1.13.0
GITLEAKS_VERSION      := v8.30.1
MISSPELL_VERSION      := v0.8.0
LEFTHOOK_VERSION      := v2.2.2
GOVULNCHECK_VERSION   := v1.9.0
VACUUM_VERSION        := v0.36.0

GOLANGCI_LINT_MODULE  := github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GOTESTSUM_MODULE      := gotest.tools/gotestsum
GITLEAKS_MODULE       := github.com/zricethezav/gitleaks/v8
MISSPELL_MODULE       := github.com/golangci/misspell/cmd/misspell
LEFTHOOK_MODULE       := github.com/evilmartians/lefthook/v2
GOVULNCHECK_MODULE    := golang.org/x/vuln/cmd/govulncheck
VACUUM_MODULE         := github.com/daveshanley/vacuum

# Images are pinned as name:tag@digest. lychee is the official image at its
# latest release.

LYCHEE_IMAGE_TAG      := lycheeverse/lychee:0.24.2

# A digest is too long to share an 80-column line with its name, so each has
# a define block of its own.

define LYCHEE_IMAGE_DIGEST
sha256:e2d19e57cf6ab037026f20b8e449a1f30d9d7f81eef4194763aab2eab20bd28d
endef


# --- Go Tools ----------------------------------------------------------------

export GOLANGCI_LINT := \
	$(TOOLS_DIR)/golangci-lint-$(GOLANGCI_LINT_VERSION)/golangci-lint
$(GOLANGCI_LINT):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(GOLANGCI_LINT_MODULE)@$(GOLANGCI_LINT_VERSION)

export GOTESTSUM := $(TOOLS_DIR)/gotestsum-$(GOTESTSUM_VERSION)/gotestsum
$(GOTESTSUM):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(GOTESTSUM_MODULE)@$(GOTESTSUM_VERSION)

export GITLEAKS := $(TOOLS_DIR)/gitleaks-$(GITLEAKS_VERSION)/gitleaks
$(GITLEAKS):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(GITLEAKS_MODULE)@$(GITLEAKS_VERSION)

export MISSPELL := $(TOOLS_DIR)/misspell-$(MISSPELL_VERSION)/misspell
$(MISSPELL):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(MISSPELL_MODULE)@$(MISSPELL_VERSION)

export LEFTHOOK := $(TOOLS_DIR)/lefthook-$(LEFTHOOK_VERSION)/lefthook
$(LEFTHOOK):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(LEFTHOOK_MODULE)@$(LEFTHOOK_VERSION)

export GOVULNCHECK := \
	$(TOOLS_DIR)/govulncheck-$(GOVULNCHECK_VERSION)/govulncheck
$(GOVULNCHECK):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(GOVULNCHECK_MODULE)@$(GOVULNCHECK_VERSION)

export VACUUM := $(TOOLS_DIR)/vacuum-$(VACUUM_VERSION)/vacuum
$(VACUUM):
	echo '>> installing $(@F)'
	GOBIN=$(@D) go install $(VACUUM_MODULE)@$(VACUUM_VERSION)

TOOLS := $(GOLANGCI_LINT) $(GOTESTSUM) $(GITLEAKS) $(MISSPELL) $(LEFTHOOK) \
	$(GOVULNCHECK) $(VACUUM)


# --- Images ------------------------------------------------------------------

export LYCHEE_IMAGE    := $(LYCHEE_IMAGE_TAG)@$(LYCHEE_IMAGE_DIGEST)
