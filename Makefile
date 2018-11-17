build:
	go build -o ci-chat-bot .
.PHONY: build

update-deps:
	dep ensure
.PHONY: update-deps
