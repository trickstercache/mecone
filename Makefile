# Copyright 2026 The Trickster Authors
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

DEFAULT: build

GO                   ?= go
MECONE_MAIN          := ./cmd/mecone
BUILD_SUBDIR         := bin
BUILD_TIME           := $(shell date -u +%FT%T%z)
GIT_LATEST_COMMIT_ID ?= $(shell git rev-parse HEAD 2>/dev/null)
TAGVER               ?= $(shell git describe --tags --dirty --always 2>/dev/null)
CGO_ENABLED          ?= 0
LDFLAGS               = -ldflags "-extldflags '-static' -w -s -X main.applicationBuildTime=$(BUILD_TIME) -X main.applicationGitCommitID=$(GIT_LATEST_COMMIT_ID) -X main.applicationVersion=$(TAGVER)"
GO_TEST_FLAGS        ?= -coverprofile=.coverprofile
LINT_FLAGS           ?=

.PHONY: go-mod-tidy
go-mod-tidy:
	$(GO) mod tidy

.PHONY: build
build: go-mod-tidy
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(LDFLAGS) -o ./$(BUILD_SUBDIR)/mecone $(MECONE_MAIN)

.PHONY: install
install:
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install $(LDFLAGS) $(MECONE_MAIN)

.PHONY: test
test:
	$(GO) test -timeout=5m $(GO_TEST_FLAGS) ./...
	@$(GO) tool cover -func=.coverprofile | tail -1

.PHONY: test-cover
test-cover: test
	$(GO) tool cover -html=.coverprofile

.PHONY: style
style:
	! gofmt -d $$(find . -name '*.go' -print) | grep '^'

.PHONY: gofix-apply
gofix-apply:
	@$(GO) fix ./...

.PHONY: gofix-diff
gofix-diff:
	@out="$$($(GO) fix -diff ./...)"; \
	if [ -n "$$out" ]; then echo "$$out"; echo "run 'make gofix-apply' to apply"; exit 1; fi

.PHONY: vulncheck
vulncheck:
	@$(GO) tool govulncheck ./...

.PHONY: golangci-lint
golangci-lint:
	@$(GO) tool golangci-lint run $(LINT_FLAGS) -c .golangci.yml

.PHONY: lint
lint: vulncheck gofix-diff golangci-lint

.PHONY: lint-fix
lint-fix:
	@$(GO) fix ./...
	@LINT_FLAGS="--fix" $(MAKE) golangci-lint
	@$(GO) tool golangci-lint fmt -c .golangci.yml

# Runs an origin on :8000 (HTTP/1.1 and h2c) for a proxy under test to front.
.PHONY: run-origin
run-origin:
	$(GO) run $(MECONE_MAIN) origin -listen :8000

# reportPaths sets $$dir (YYYY-MM-DD-hhmmss) and $$ts (yymmddhhmmss) from one clock reading, then
# writes the three report files under catalogs/$(1)/reports/$$dir/. $(2) is json or yaml for the
# full results file.
define reportPaths
	now=$$(date +%Y%m%d%H%M%S); \
	ts=$${now:2}; \
	dir=$${now:0:4}-$${now:4:2}-$${now:6:2}-$${now:8:6}; \
	mkdir -p catalogs/$(1)/reports/$$dir; \
	$(GO) run $(MECONE_MAIN) run -config catalogs/$(1)/mecone.yaml \
		-$(2) catalogs/$(1)/reports/$$dir/report-$$ts.$(2) \
		-csv catalogs/$(1)/reports/$$dir/report-$$ts.csv \
		-md catalogs/$(1)/reports/$$dir/report-$$ts.md
endef

# Runs every suite straight against Mecone's own origin, with no proxy in between, writing a JSON,
# CSV and Markdown report per run. It exercises the harness rather than measuring a proxy.
.PHONY: selftest
selftest:
	@$(call reportPaths,selftest,json)

# Tests every proxy in the industry-wide catalog, writing a YAML, CSV and Markdown report per run.
.PHONY: industry-test
industry-test:
	@$(call reportPaths,industry-wide,yaml)

# Tests the newest Trickster release and the one running on this machine, writing a JSON, CSV and
# Markdown report per run. See catalogs/trickster-baseline/mecone.yaml for the local setup it needs.
.PHONY: trickster-baseline-test
trickster-baseline-test:
	@$(call reportPaths,trickster-baseline,json)

.PHONY: trickster-dev-test
trickster-dev-test:
	@$(call reportPaths,trickster-dev,json)

.PHONY: clean
clean:
	rm -rf ./$(BUILD_SUBDIR) .coverprofile
