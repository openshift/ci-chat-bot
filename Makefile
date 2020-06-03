help:
	@echo "Targets:"
	@echo -e " - help: \tHey! You're reading it now!"
	@echo -e " - build:\tbuilds ci-chat-bot"
	@echo -e " - clean:\tremoves generated files"
	@echo -e " - update-deps:\tupdates vendored dependencies"
	@echo -e " - run:  \truns ci-chat-bot"

.PHONY: help

build:
	go build -ldflags "-X k8s.io/client-go/pkg/version.gitVersion=$$(git describe --abbrev=8 --dirty --always)" -mod vendor -o ci-chat-bot .
.PHONY: build

clean:
	rm -f ci-chat-bot
.PHONY: clean

update-deps:
	GO111MODULE=on go mod vendor
.PHONY: update-deps

run:
	./hack/run.sh
.PHONY: run
