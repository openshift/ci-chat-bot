# Version information
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TREE_STATE ?= $(shell if git diff --quiet 2>/dev/null; then echo "clean"; else echo "dirty"; fi)
BUILD_DATE ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Build flags for version information
LDFLAGS := -X github.com/openshift-eng/cyborg-data/go.Version=$(VERSION) \
           -X github.com/openshift-eng/cyborg-data/go.GitCommit=$(GIT_COMMIT) \
           -X github.com/openshift-eng/cyborg-data/go.GitTreeState=$(GIT_TREE_STATE) \
           -X github.com/openshift-eng/cyborg-data/go.BuildDate=$(BUILD_DATE)

all: test examples
.PHONY: all

# Build all examples
# NOTE: Production deployments require -tags gcs for GCS support
examples: gcs-example comprehensive-example
.PHONY: examples

# Individual example targets
gcs-example:
	cd example/with-gcs && go build -tags gcs -ldflags "$(LDFLAGS)" -o ./with-gcs .
.PHONY: gcs-example

gcs-example-stub:
	cd example/with-gcs && go build -ldflags "$(LDFLAGS)" -o ./with-gcs-stub .
.PHONY: gcs-example-stub

comprehensive-example:
	cd example/comprehensive && go build -ldflags "$(LDFLAGS)" -o ./comprehensive .
.PHONY: comprehensive-example

# Test targets
test:
	go test ./...
.PHONY: test

test-with-gcs:
	go test -tags gcs ./...
.PHONY: test-with-gcs

test-verbose:
	go test -v ./...
.PHONY: test-verbose

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
.PHONY: test-coverage

# Benchmarks
bench:
	go test -bench=. ./...
.PHONY: bench

bench-with-gcs:
	go test -tags gcs -bench=. ./...
.PHONY: bench-with-gcs

# Dependency management
vendor:
	go mod tidy
	go mod vendor
.PHONY: vendor

tidy:
	go mod tidy
.PHONY: tidy

# Linting
lint:
	golangci-lint run --timeout=20m
.PHONY: lint

lint-with-gcs:
	golangci-lint run --timeout=20m --build-tags "gcs"
.PHONY: lint-with-gcs

# Format code
fmt:
	go fmt ./...
.PHONY: fmt

# Vet code
vet:
	go vet ./...
.PHONY: vet

vet-with-gcs:
	go vet -tags gcs ./...
.PHONY: vet-with-gcs

# Clean up
clean:
	rm -f example/with-gcs/with-gcs example/with-gcs/with-gcs-stub example/comprehensive/comprehensive
	rm -f coverage.out coverage.html
.PHONY: clean

# Show version info
version:
	@echo "Version:       $(VERSION)"
	@echo "Git Commit:    $(GIT_COMMIT)"
	@echo "Git Tree:      $(GIT_TREE_STATE)"
	@echo "Build Date:    $(BUILD_DATE)"
.PHONY: version

# Help
help:
	@echo "Available targets:"
	@echo "  all                    - Run tests and build all examples"
	@echo "  examples               - Build all examples (requires -tags gcs for production)"
	@echo "  gcs-example            - Build GCS example with full SDK support"
	@echo "  gcs-example-stub       - Build GCS example in stub mode (no tags)"
	@echo "  comprehensive-example  - Build comprehensive demo"
	@echo "  test                   - Run unit tests"
	@echo "  test-with-gcs          - Run unit tests with GCS build tags"
	@echo "  test-verbose           - Run tests with verbose output"
	@echo "  test-coverage          - Run tests with coverage report"
	@echo "  bench                  - Run benchmarks"
	@echo "  bench-with-gcs         - Run benchmarks with GCS build tags"
	@echo "  vendor                 - Update dependencies and vendor"
	@echo "  tidy                   - Run go mod tidy"
	@echo "  lint                   - Run linter"
	@echo "  lint-with-gcs          - Run linter with GCS build tags"
	@echo "  fmt                    - Format code"
	@echo "  vet                    - Run go vet"
	@echo "  vet-with-gcs           - Run go vet with GCS build tags"
	@echo "  clean                  - Remove built binaries"
	@echo "  version                - Show version information"
	@echo "  help                   - Show this help"
	@echo ""
	@echo "NOTE: Production builds require -tags gcs for GCS support"
.PHONY: help
