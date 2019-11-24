build:
	go build -o ci-chat-bot .
.PHONY: build

update-deps:
	GO111MODULE=on go mod vendor
.PHONY: update-deps
