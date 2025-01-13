FROM registry.ci.openshift.org/openshift/centos:stream9
LABEL maintainer="apavel@redhat.com"
ADD ci-chat-bot /usr/bin/ci-chat-bot
RUN curl -s -L https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/rosa/1.2.49/rosa-linux.tar.gz | tar -xvz -C /usr/bin --no-same-owner
ENTRYPOINT ["/usr/bin/ci-chat-bot"]
