FROM registry.ci.openshift.org/openshift/centos:stream9
LABEL maintainer="apavel@redhat.com"
ADD ci-chat-bot /usr/bin/ci-chat-bot
# Commenting this out until v1.2.29 is officially released...
# RUN curl -s -L https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/rosa/1.2.29/rosa-linux.tar.gz | tar -xvz -C /usr/bin
RUN curl -s -L https://github.com/openshift/rosa/releases/download/v1.2.29/rosa-linux-amd64 > /usr/bin/rosa && chmod 755 /usr/bin/rosa
ENTRYPOINT ["/usr/bin/ci-chat-bot"]
