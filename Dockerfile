FROM registry.ci.openshift.org/openshift/origin-v4.0:base
ADD ci-chat-bot /usr/bin/ci-chat-bot
ENTRYPOINT ["/usr/bin/ci-chat-bot"]
