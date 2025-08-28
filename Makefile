# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
)

# Build configuration
git_commit=$(shell git describe --tags --always --dirty)
build_date=$(shell date -u '+%Y%m%d')
version=v${build_date}-${git_commit}

# Default build flags (can be overridden via BUILD_FLAGS env var)
BUILD_FLAGS ?=

SOURCE_GIT_TAG=v1.0.0+$(shell git rev-parse --short=7 HEAD)

GO_LD_EXTRAFLAGS=-X github.com/openshift/ci-chat-bot/vendor/k8s.io/client-go/pkg/version.gitCommit=$(shell git rev-parse HEAD) -X github.com/openshift/ci-chat-bot/vendor/k8s.io/client-go/pkg/version.gitVersion=${SOURCE_GIT_TAG} -X sigs.k8s.io/prow/version.Name=ci-chat-bot -X sigs.k8s.io/prow/version.Version=${version}
GOLINT=golangci-lint run

# Support for build flags (e.g., -tags gcs)
GO_BUILD_FLAGS=$(BUILD_FLAGS)

debug:
	go build ${GO_BUILD_FLAGS} -gcflags="all=-N -l" ${GO_LD_FLAGS} -mod vendor -o ci-chat-bot ./cmd/...
.PHONY: debug

# Override build target to support BUILD_FLAGS
build:
	go build ${GO_BUILD_FLAGS} ${GO_LD_FLAGS} -mod vendor -o ci-chat-bot ./cmd/...
.PHONY: build

vendor:
	go mod tidy
	go mod vendor
.PHONY: vendor

validate-vendor: vendor
	git status -s ./vendor/ go.mod go.sum
	test -z "$$(git status -s ./vendor/ go.mod go.sum | grep -v vendor/modules.txt)"
.PHONY: validate-vendor

run:
	./hack/run.sh
.PHONY: run

run-gcs:
	./hack/run-with-gcs.sh
.PHONY: run-gcs

run-local:
	USE_GCS_ORGDATA=false ./hack/run.sh
.PHONY: run-local

help-ci-chat-bot:
	@echo "CI Chat Bot specific targets:"
	@echo "  build            - Build ci-chat-bot binary"
	@echo "  debug            - Build with debug symbols"
	@echo "  run              - Run ci-chat-bot with hack/run.sh (auto-detects GCS vs local)"
	@echo "  run-gcs          - Run with GCS backend explicitly"
	@echo "  run-local        - Run with local file backend explicitly"
	@echo ""
	@echo "Build flags:"
	@echo "  BUILD_FLAGS      - Pass build flags (e.g., BUILD_FLAGS='-tags gcs' make build)"
	@echo ""
	@echo "Environment variables for hack scripts:"
	@echo "  USE_GCS_ORGDATA  - Set to 'true' to use GCS backend"
	@echo "  GCS_BUCKET       - GCS bucket name (default: resolved-org)"
	@echo "  GCS_PROJECT_ID   - GCS project ID (default: openshift-crt-mce)"
	@echo "  ORGDATA_PATHS    - Local orgdata file path"
	@echo "  AUTH_CONFIG      - Authorization config file path"
	@echo ""
	@echo "Examples:"
	@echo "  make BUILD_FLAGS='-tags gcs' build    # Build with GCS support"
	@echo "  make run-gcs                          # Run with GCS backend"
	@echo "  ORGDATA_PATHS=/my/file.json make run  # Run with custom local file"
.PHONY: help-ci-chat-bot

lint: verify-golint

sonar-reports:
	go test ./... -coverprofile=coverage.out -covermode=count -json > report.json
	golangci-lint run ./... --verbose --no-config --out-format checkstyle --issues-exit-code 0 > golangci-lint.out
.PHONY: sonar-reports
