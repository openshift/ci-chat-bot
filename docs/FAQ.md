# Frequently Asked Questions

1. **How can I launch a cluster built from multiple PRs, on GCP?**

    `launch openshift/origin#49563,openshift/kubernetes#731,openshift/machine-api-operator#831 gcp`


2. **How can I update the "Global Cluster Pull Secret", on a cluster-bot created cluster, to pull from one of the CI Build Farms?**

   1. Authenticate to one of the CI Registires and update your pull secret as defined [here](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/#how-do-i-log-in-to-pull-images-that-require-authentication)
   2. Follow the instructions [here](https://docs.openshift.com/container-platform/4.7/support/remote_health_monitoring/opting-out-of-remote-health-reporting.html#images-update-global-pull-secret_opting-out-remote-health-reporting).


3. **How can I access to cluster-bot created metal IPI cluster via web browser**

   There are a couple different options you can choose from:

   a. Browser proxy
   1. Copy IP address and port number from kubeconfig's `proxy-url` field
   2. Change web browser's proxy settings with these values
   
   b. [Dev-scripts](https://github.com/openshift-metal3/dev-scripts/#gui)


4. **It's been more than 30mins I did not get auth credentials yet, what do I do?**

   Issuing an `auth` command will attempt to fetch the credentials for the cluster.  It will return a "your cluster is still getting created" message or the cluster's kube-config file if the cluster has launched successfully.

## workflow_launch
1. **What is `workflow_launch`?**

   As ci-chat-bot (a.k.a. cluster-bot) gets more use and users want to add more variants that can be run, CRT has been working on an easier way of adding and running these new variants. The `workflow_launch` command has been added to simplify the process of running new cluster types. 

   The definition of the command is as follows:

   `workflow-launch {workflow_name} {image_or_version_or_pr} {parameters}`

   The parameters option in the slack command is a list of double-quoted environment variable settings separated by commas. For instance, if I want to launch a 4.9 cluster using the openshift-e2e-azure workflow with a compact cluster size and preserved bootstrap resources, I would run this command:

   `workflow-launch openshift-e2e-azure 4.9 "SIZE_VARIANT=compact","OPENSHIFT_INSTALL_PRESERVE_BOOTSTRAP=1"`

   To add a workflow to be supported by the command, the workflow must be added to the workflow config file via a PR to `openshift/release`. For most workflows, only the following will need to be added:
   ```
   workflow_name:
   platform: supported_platform
   ```

   The list of supported platforms can be found in the ci-chat-bot's code.

   The workflow config file also supports setting base_images for workflows that may require special base images to run as well as an architecture field that will support configuring the CPU architecture for openshift to run on once non-amd64 architectures are supported by the ci-chat-bot (https://github.com/openshift/ci-chat-bot/blob/master/prow.go#L190).


2. **How can I make a workflow runnable by the `workflow-launch` command**

   1. Make a PR editing the
      [workflows-config.yaml](https://github.com/openshift/release/blob/master/core-services/ci-chat-bot/workflows-config.yaml)
      file in the `openshift/release` repository. Add the workflow as an object
      under the `workflows` field and set its `platform` field to a platform
      suported by the ci-chat-bot (e.g. `aws`, `gcp`, etc.). You can specify
      `base_images` that your workflow requires if necessary, using the same object
      definition as in a `ci-operator` config file. An `architecture` field also
      exists that can be used to configure the architecture to run openshift on
      once `ci-chat-bot` adds support outside of `amd64`.


## Metalkube and Bare-Metal deployments Access
1. How do I access a bare-metal platform created by ci-chat-bot (links provided to the platform appear invalid/unroutable)

    ci-chat-bot when given a launch paramter that includes `metal` will deploy an instance that requires access via a proxy that is not immediately accessible via Red Hat internal networks or VPN. Access requires exporting httpProxy and httpsProxy values as defined by the bot output on successful cluster deployment. 
    Example Output and suggested usage:
>    Your cluster is ready, it will be shut down automatically in ~158 minutes.
https://console-openshift-console.apps.ostest.test.metalkube.org
Log in to the console with user kubeadmin and password <redacted>
cluster-bot-2022-11-15-152108.kubeconfig
apiVersion: v1
clusters:
- cluster:
    proxy-url: http://145.40.68.183:8213/


    - `http_proxy=145.40.68.183:8213 https_proxy=145.40.68.183:8213 curl -L https://console-openshift-console.apps.ostest.test.metalkube.org/ -v`
    - `curl -kvx http://145.40.68.183:8213 https://console-openshift-console.apps.ostest.test.metalkube.org/dashboards`
    - `export http_proxy=145.40.68.183:8213 && export https_proxy=145.40.68.183:8213 && oc login -u kubeadmin -p <password> https://api.ostest.test.metalkube.org:6443`


