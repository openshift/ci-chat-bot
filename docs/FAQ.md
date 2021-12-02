# Frequently Asked Questions

1. **How can I launch a cluster built from multiple PRs, on GCP?**

    `launch openshift/origin#49563,openshift/kubernetes#731,openshift/machine-api-operator#831 gcp`


2. **How can I update the "Global Cluster Pull Secret", on a cluster-bot created cluster, to pull from one of the CI Build Farms?**

   1. Authenticate to one of the CI Registires and update your pull secret as defined [here](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/#how-do-i-log-in-to-pull-images-that-require-authentication)
   2. Follow the instructions [here](https://docs.openshift.com/container-platform/4.7/support/remote_health_monitoring/opting-out-of-remote-health-reporting.html#images-update-global-pull-secret_opting-out-remote-health-reporting).

3. **How can I access to cluster-bot created metal IPI cluster via web browser**

   1. Copy IP address and port number from kubeconfig's `proxy-url` field
   2. Change web browser's proxy settings with these values

4. **How can I make a workflow runnable by the `workflow-launch` command**

   1. Make a PR editing the
   [workflows-config.yaml](https://github.com/openshift/release/blob/master/core-services/ci-chat-bot/workflows-config.yaml)
   file in the `openshift/release` repository. Add the workflow as an object
   under the `workflows` field and set its `platform` field to a platform
   suported by the ci-chat-bot (e.g. `aws`, `gcp`, etc.). You can specify
   `base_images` that your workflow requires if necessary, using the same object
   definition as in a `ci-operator` config file. An `architecture` field also
   exists that can be used to configure the architecture to run openshift on
   once `ci-chat-bot` adds support outside of `amd64`.
