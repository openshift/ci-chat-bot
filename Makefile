build:
	go build -mod vendor -o ci-chat-bot .
.PHONY: build

update-deps:
	GO111MODULE=on go mod vendor
.PHONY: update-deps

run:
	./hack/run.sh
.PHONY: run
