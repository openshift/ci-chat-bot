package api

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	RegistryPullCredentialsSecret = "registry-pull-credentials"

	RegistryPushCredentialsCICentralSecret          = "registry-push-credentials-ci-central"
	RegistryPushCredentialsCICentralSecretMountPath = "/etc/push-secret"

	GCSUploadCredentialsSecret          = "gce-sa-credentials-gcs-publisher"
	GCSUploadCredentialsSecretMountPath = "/secrets/gcs"

	ManifestToolLocalPusherSecret          = "manifest-tool-local-pusher"
	ManifestToolLocalPusherSecretMountPath = "/secrets/manifest-tool"

	ReleaseAnnotationSoftDelete = "release.openshift.io/soft-delete"

	// DPTPRequesterLabel is the label on a Kubernates CR whose value indicates the automated tool that requests the CR
	DPTPRequesterLabel = "dptp.openshift.io/requester"

	KVMDeviceLabel           = "devices.kubevirt.io/kvm"
	ClusterLabel             = "ci-operator.openshift.io/cluster"
	CloudLabel               = "ci-operator.openshift.io/cloud"
	CloudClusterProfileLabel = "ci-operator.openshift.io/cloud-cluster-profile"

	NoBuildsLabel = "ci.openshift.io/no-builds"
	NoBuildsValue = "true"

	// HiveCluster is the cluster where Hive is deployed
	HiveCluster = ClusterHive

	// HiveAdminKubeconfigSecret is the name of the secret in ci-op-<hash> namespace that stores the Admin's kubeconfig for the ephemeral cluster provisioned by Hive.
	HiveAdminKubeconfigSecret = "hive-admin-kubeconfig"
	// HiveAdminKubeconfigSecretKey is the key to the kubeconfig in the secret HiveAdminKubeconfigSecret
	HiveAdminKubeconfigSecretKey = "kubeconfig"
	// HiveAdminPasswordSecret the name of the secret in ci-op-<hash> namespace that stores the password for the user "kubeadmin" for the ephemeral cluster provisioned by Hive.
	HiveAdminPasswordSecret = "hive-admin-password"
	// HiveAdminPasswordSecretKey is the key to the password in the secret HiveAdminKubeconfigSecret
	HiveAdminPasswordSecretKey = "password"

	// HiveControlPlaneKubeconfigSecret is the name of the secret that stores kubeconfig to contact the cluster where Hive is deployed
	HiveControlPlaneKubeconfigSecret = "hive-hive-credentials"
	// HiveControlPlaneKubeconfigSecretArg is the flag to ci-operator
	HiveControlPlaneKubeconfigSecretArg = "--hive-kubeconfig=/secrets/hive-hive-credentials/kubeconfig"

	AutoScalePodsLabel = "ci.openshift.io/scale-pods"

	NamespaceDir = "build-resources"

	APPCIKubeAPIURL = "https://api.ci.l2s4.p1.openshiftapps.com:6443"

	// ReasonPending is the error reason for pods not scheduled in time.
	// It is generated when pods are for whatever reason not scheduled before
	// `podStartTimeout`.
	ReasonPending = "pod_pending"
	// CliEnv if the env we use to expose the path to the cli
	CliEnv                = "CLI_DIR"
	DefaultLeaseEnv       = "LEASED_RESOURCE"
	DefaultIPPoolLeaseEnv = "IP_POOL_AVAILABLE"
	// SkipCensoringLabel is the label we use to mark a secret as not needing to be censored
	SkipCensoringLabel = "ci.openshift.io/skip-censoring"

	OauthTokenSecretKey  = "oauth"
	OauthTokenSecretName = "github-credentials-openshift-ci-robot-private-git-cloner"

	CIAdminsGroupName = "test-platform-ci-admins"

	ShmResource       = "ci-operator.openshift.io/shm"
	NvidiaGPUResource = "nvidia.com/gpu"

	// ReleaseConfigAnnotation is the name of annotation created by the release controller.
	// ci-operator uses the release controller configuration to determine
	// the version of OpenShift we create from the ImageStream, so we need
	// to copy the annotation if it exists
	ReleaseConfigAnnotation = "release.openshift.io/config"

	ImageStreamImportRetries = 6
)

var (
	clusterNames = sets.New[string](
		string(ClusterAPPCI),
		string(ClusterARM01),
		string(ClusterBuild01),
		string(ClusterBuild02),
		string(ClusterBuild03),
		string(ClusterVSphere02),
	)
)

// GitHubUserGroup returns the group name for a GitHub user
func GitHubUserGroup(username string) string {
	return fmt.Sprintf("%s-group", username)
}

// ValidClusterName checks if a cluster name is valid
func ValidClusterName(clusterName string) bool {
	return clusterNames.Has(clusterName) || buildClusterRegEx.MatchString(clusterName)
}
