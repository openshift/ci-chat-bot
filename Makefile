build:
	go build -ldflags "-X k8s.io/client-go/pkg/version.gitVersion=$$(git describe --abbrev=8 --dirty --always)" -mod vendor -o ci-chat-bot .
.PHONY: build

debug:
	go build -gcflags="all=-N -l" -ldflags "-X k8s.io/client-go/pkg/version.gitVersion=$$(git describe --abbrev=8 --dirty --always)" -mod vendor -o ci-chat-bot .
.PHONY: build

update-deps:
	GO111MODULE=on go mod vendor
.PHONY: update-deps

run:
	./hack/run.sh
.PHONY: run
