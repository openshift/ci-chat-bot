FROM registry.ci.openshift.org/openshift/release:golang-1.18 AS builder
WORKDIR /go/src/github.com/openshift/ci-chat-bot
COPY . .
RUN make

FROM registry.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/ci-chat-bot/ci-chat-bot /usr/bin/
RUN curl -s -L https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/rosa/1.2.15/rosa-linux.tar.gz | tar -xvz -C /usr/bin
ENTRYPOINT ["/usr/bin/ci-chat-bot"]
