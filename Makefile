# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
)

# Build configuration
git_commit=$(shell git describe --tags --always --dirty)
build_date=$(shell date -u '+%Y%m%d')
version=v${build_date}-${git_commit}

# Use standard GO_BUILD_FLAGS for build tags (e.g., -tags gcs)
GO_BUILD_FLAGS ?=

# Extend OpenShift's standard ldflags
GO_LD_EXTRAFLAGS=-X github.com/openshift/ci-chat-bot/vendor/k8s.io/client-go/pkg/version.gitCommit=$(shell git rev-parse HEAD) -X github.com/openshift/ci-chat-bot/vendor/k8s.io/client-go/pkg/version.gitVersion=${SOURCE_GIT_TAG} -X sigs.k8s.io/prow/version.Name=ci-chat-bot -X sigs.k8s.io/prow/version.Version=${version}
GOLINT=golangci-lint run

# Build with GCS support enabled
build-gcs:
	$(MAKE) GO_BUILD_FLAGS="-tags gcs" build
.PHONY: build-gcs

debug:
	go build $(GO_BUILD_FLAGS) -gcflags="all=-N -l" $(GO_LD_FLAGS) $(GO_MOD_FLAGS) -o ci-chat-bot ./cmd/...
.PHONY: debug

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
	@echo "  build            - Build ci-chat-bot binary (standard OpenShift build)"
	@echo "  build-gcs        - Build with GCS support enabled (-tags gcs)"
	@echo "  debug            - Build with debug symbols"
	@echo "  run              - Run ci-chat-bot with hack/run.sh (auto-detects GCS vs local)"
	@echo "  run-gcs          - Run with GCS backend explicitly"
	@echo "  run-local        - Run with local file backend explicitly"
	@echo ""
	@echo "Build flags:"
	@echo "  GO_BUILD_FLAGS   - Standard OpenShift build flags (e.g., GO_BUILD_FLAGS='-tags gcs' make build)"
	@echo ""
	@echo "Environment variables for hack scripts:"
	@echo "  USE_GCS_ORGDATA  - Set to 'true' to use GCS backend"
	@echo "  GCS_BUCKET       - GCS bucket name (default: resolved-org)"
	@echo "  GCS_PROJECT_ID   - GCS project ID (default: openshift-crt-mce)"
	@echo "  ORGDATA_PATHS    - Local orgdata file path"
	@echo "  AUTH_CONFIG      - Authorization config file path"
	@echo ""
	@echo "Examples:"
	@echo "  make build-gcs                            # Build with GCS support (recommended)"
	@echo "  make GO_BUILD_FLAGS='-tags gcs' build     # Build with GCS using OpenShift standard"
	@echo "  make run-gcs                              # Run with GCS backend"
	@echo "  ORGDATA_PATHS=/my/file.json make run      # Run with custom local file"
.PHONY: help-ci-chat-bot

lint: verify-golint

sonar-reports:
	go test ./... -coverprofile=coverage.out -covermode=count -json > report.json
	golangci-lint run ./... --verbose --no-config --out-format checkstyle --issues-exit-code 0 > golangci-lint.out
.PHONY: sonar-reports
