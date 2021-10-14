# Frequently Asked Questions

1. **How can I launch a cluster built from multiple PRs, on GCP?**
   
    `launch openshift/origin#49563,openshift/kubernetes#731,openshift/machine-api-operator#831 gcp`
   

2. **How can I update the "Global Cluster Pull Secret", on a cluster-bot created cluster, to pull from one of the CI Build Farms?**

   1. Authenticate to one of the CI Registires and update your pull secret as defined [here](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/#how-do-i-log-in-to-pull-images-that-require-authentication) 
   2. Follow the instructions [here](https://docs.openshift.com/container-platform/4.7/support/remote_health_monitoring/opting-out-of-remote-health-reporting.html#images-update-global-pull-secret_opting-out-remote-health-reporting).


3. **It's been more than 30mins I did not get auth credentials yet, what do I do?**
   /msg auth
   It will return message "your cluster is still getting created" or the kube-config file in case cluster is launched successfully
