FROM registry.svc.ci.openshift.org/openshift/release:golang-1.10 AS builder
WORKDIR /go/src/github.com/openshift/ci-chat-bot
COPY . .
RUN make

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/ci-chat-bot/ci-chat-bot /usr/bin/
ENTRYPOINT ["/usr/bin/ci-chat-bot"]
