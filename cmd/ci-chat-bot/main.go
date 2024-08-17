package main

import (
	"context"
	"flag"
	"fmt"
	"k8s.io/client-go/tools/cache"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/adrg/xdg"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	botversion "github.com/openshift/ci-chat-bot/pkg/version"

	"github.com/openshift/rosa/pkg/rosa"

	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"

	clientset "k8s.io/client-go/kubernetes"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	buildconfigclientset "github.com/openshift/client-go/build/clientset/versioned"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"

	"gopkg.in/yaml.v2"

	"github.com/spf13/pflag"

	citools "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog"
	prowclientset "sigs.k8s.io/prow/pkg/client/clientset/versioned"
	prowinformers "sigs.k8s.io/prow/pkg/client/informers/externalversions"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
)

const (
	controllerDefaultResyncDuration = 24 * time.Hour
)

var (
	clusterBotMetrics = metrics.NewMetrics("cluster_bot")
)

type options struct {
	prowconfig               configflagutil.ConfigOptions
	GitHubOptions            prowflagutil.GitHubOptions
	KubernetesOptions        prowflagutil.KubernetesOptions
	InstrumentationOptions   prowflagutil.InstrumentationOptions
	ForcePROwner             string
	ReleaseClusterKubeconfig string
	ConfigResolver           string
	WorkflowConfigPath       string
	Port                     int
	GracePeriod              time.Duration

	leaseServer                string
	leaseServerCredentialsFile string
	leaseClient                manager.LeaseClient

	rosaClusterLimit         int
	rosaClusterAdminUsername string
	rosaSubnetListPath       string
	rosaOIDCConfigId         string
	rosaBillingAccount       string
	overrideLaunchLabel      string
	overrideRosaSecretName   string

	jiraOptions prowflagutil.JiraOptions
}

func (o *options) Validate() error {
	if o.ReleaseClusterKubeconfig != "" {
		if _, err := os.Stat(o.ReleaseClusterKubeconfig); err != nil {
			return fmt.Errorf("error accessing --release-cluster-kubeconfig: %w", err)
		}
	}
	for _, group := range []flagutil.OptionGroup{&o.GitHubOptions, &o.KubernetesOptions, &o.InstrumentationOptions} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	version := botversion.Get()
	fmt.Printf("CI-Chat-Bot Version: %s, Build Date: %s\n", version.GitVersion, version.BuildDate)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	emptyFlags := flag.NewFlagSet("empty", flag.ContinueOnError)
	klog.InitFlags(emptyFlags)
	opt := &options{
		prowconfig: configflagutil.ConfigOptions{
			ConfigPathFlagName:    "prow-config",
			JobConfigPathFlagName: "job-config",
		},
		ConfigResolver:           "http://config.ci.openshift.org/config",
		KubernetesOptions:        prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true},
		rosaClusterAdminUsername: "cluster-admin",
	}

	// Initialize a standard logger for k8s controller-runtime
	ctrlruntimelog.SetLogger(logr.New(ctrlruntimelog.NullLogSink{}))

	pflag.StringVar(&opt.ConfigResolver, "config-resolver", opt.ConfigResolver, "A URL pointing to a config resolver for retrieving ci-operator config. You may pass a location on disk with file://<abs_path_to_ci_operator_config>")
	pflag.StringVar(&opt.ForcePROwner, "force-pr-owner", opt.ForcePROwner, "Make the supplied user the owner of all PRs for access control purposes.")
	pflag.StringVar(&opt.ReleaseClusterKubeconfig, "release-cluster-kubeconfig", "", "Kubeconfig to use for cluster housing the release imagestreams. Defaults to normal kubeconfig if unset.")
	pflag.StringVar(&opt.WorkflowConfigPath, "workflow-config-path", "", "Path to config file used for workflow commands")
	pflag.IntVar(&opt.Port, "port", 8080, "Port to listen on.")
	pflag.DurationVar(&opt.GracePeriod, "grace-period", 5*time.Second, "On shutdown, try to handle remaining events for the specified duration.")
	pflag.StringVar(&opt.leaseServer, "lease-server", citools.URLForService(citools.ServiceBoskos), "Address of the server that manages leases. Used to identify accounts with more available leases.")
	pflag.StringVar(&opt.leaseServerCredentialsFile, "lease-server-credentials-file", "", "The path to credentials file used to access the lease server. The content is of the form <username>:<password>.")
	pflag.StringVar(&opt.overrideLaunchLabel, "override-launch-label", "", "Override the default launch label for jobs. Used for local debugging.")
	pflag.StringVar(&opt.overrideRosaSecretName, "override-rosa-secret-name", "", "Override the default secret name for rosa cluster tracking. Used for local debugging.")
	pflag.IntVar(&opt.rosaClusterLimit, "rosa-cluster-limit", 15, "Maximum number of ROSA clusters that can exist at the same time. Set to 0 for no limit.")
	pflag.StringVar(&opt.rosaClusterAdminUsername, "rosa-cluster-admin-username", "cluster-admin", "Admin username of a ROSA cluster")
	pflag.StringVar(&opt.rosaSubnetListPath, "rosa-subnetlist-path", "", "Path to list of comma-separated subnets to use for ROSA hosted clusters.")
	pflag.StringVar(&opt.rosaOIDCConfigId, "rosa-oidcConfigId-path", "", "Path to the OIDC configuration ID")
	pflag.StringVar(&opt.rosaBillingAccount, "rosa-billingAccount-path", "", "Path to the Billing Account ID.")

	opt.prowconfig.AddFlags(emptyFlags)
	opt.GitHubOptions.AddFlags(emptyFlags)
	opt.KubernetesOptions.AddFlags(emptyFlags)
	opt.InstrumentationOptions.AddFlags(emptyFlags)
	opt.jiraOptions.AddFlags(emptyFlags)
	pflag.CommandLine.AddGoFlagSet(emptyFlags)
	pflag.Parse()
	klog.SetOutput(os.Stderr)
	// let k8s know that we're alive
	health := pjutil.NewHealthOnPort(opt.InstrumentationOptions.HealthPort)

	// change directory to writable XDG_RUNTIME_DIR for catalog build caching purposes
	if err := os.Chdir(xdg.RuntimeDir); err != nil {
		return fmt.Errorf("couldn't change directory to $XDG_RUNTIME_DIR (%s): %w", xdg.RuntimeDir, err)
	}

	if err := opt.Validate(); err != nil {
		return fmt.Errorf("unable to validate program arguments: %w", err)
	}

	if opt.overrideLaunchLabel != "" {
		utils.LaunchLabel = opt.overrideLaunchLabel
	}
	if opt.overrideRosaSecretName != "" {
		manager.RosaClusterSecretName = opt.overrideRosaSecretName
	}

	if err := opt.KubernetesOptions.AddKubeconfigChangeCallback(func() {
		klog.Infof("received kubeconfig changed event, exiting to make the kubelet restart us so we can pick them up")
		os.Exit(0)
	}); err != nil {
		return fmt.Errorf("failed to set up kubeconfig watches: %w", err)
	}
	kubeConfigs, err := opt.KubernetesOptions.LoadClusterConfigs()
	if err != nil {
		return fmt.Errorf("could not load kube configs: %w", err)
	}
	buildClusterClientConfigs, err := processKubeConfigs(kubeConfigs)
	if err != nil {
		return fmt.Errorf("could not process kube configs: %w", err)
	}

	resolverURL, err := url.Parse(opt.ConfigResolver)
	if err != nil {
		return fmt.Errorf("--config-resolver is not a valid URL: %w", err)
	}
	resolver := &manager.URLConfigResolver{URL: resolverURL}

	botToken := os.Getenv("BOT_TOKEN")
	if len(botToken) == 0 {
		return fmt.Errorf("the environment variable BOT_TOKEN must be set")
	}

	botSigningSecret := os.Getenv("BOT_SIGNING_SECRET")
	if len(botSigningSecret) == 0 {
		return fmt.Errorf("the environment variable BOT_SIGNING_SECRET must be set")
	}

	var hasSynced []cache.InformerSynced
	prowJobKubeconfig, _, _, err := utils.LoadKubeconfig()
	if err != nil {
		return err
	}
	prowClient, err := prowclientset.NewForConfig(prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to create prow client: %w", err)
	}
	prowjobInformerFactory := prowinformers.NewSharedInformerFactory(prowClient, controllerDefaultResyncDuration)
	prowjobInformer := prowjobInformerFactory.Prow().V1().ProwJobs()
	hasSynced = append(hasSynced, prowjobInformer.Informer().HasSynced)

	// Config and Client to access release images
	releaseConfig, err := utils.LoadKubeconfigFromFlagOrDefault(opt.ReleaseClusterKubeconfig, prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to load kubeConfig from flag or default: %w", err)
	}
	imageClient, err := imageclientset.NewForConfig(releaseConfig)
	if err != nil {
		return fmt.Errorf("unable to create image client: %w", err)
	}
	releaseClient, err := corev1.NewForConfig(prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unabel to create release client: %w", err)
	}
	rosaSecretClient := releaseClient.Secrets("ci")
	// Config and Client to access hypershift configmaps
	var hiveConfigMapClient corev1.ConfigMapInterface
	if config, ok := kubeConfigs["hosted-mgmt"]; ok {
		hiveClient, err := corev1.NewForConfig(&config)
		if err != nil {
			return fmt.Errorf("unable to create hive client: %w", err)
		}
		hiveConfigMapClient = hiveClient.ConfigMaps("hypershift")
	} else {
		klog.Warning("hive config missing `hive` cluster context; will not support hypershift outside of default version")
	}

	configAgent, err := opt.prowconfig.ConfigAgent()
	if err != nil {
		return err
	}

	workflows := manager.WorkflowConfig{}
	go manageWorkflowConfig(opt.WorkflowConfigPath, &workflows)

	var ghClient github.Client

	if token := os.Getenv("GITHUB_TOKEN"); len(token) > 0 {
		ghClient, err = opt.GitHubOptions.GitHubClientWithAccessToken(token)
		if err != nil {
			return fmt.Errorf("unable to create github client: %v", err)
		}
		_, err := ghClient.BotUser()
		if err != nil {
			return fmt.Errorf("unable to get github bot user: %v", err)
		}
	} else {
		ghClient, err = opt.GitHubOptions.GitHubClient(false)
		if err != nil {
			return fmt.Errorf("unable to create github client: %w", err)
		}
	}

	// we don't care if this errors; if it fails, leaseClient will be nil, and we won't auto-distribute among accounts
	if err := opt.initializeLeaseClient(); err != nil {
		klog.Warningf("Failed to load lease client. Will not distribute jobs across secondary accounts. Error: %v", err)
	}

	// ROSA setup
	rosaClient := rosa.NewRuntime().WithAWS().WithOCM()
	defer rosaClient.Cleanup()

	rosaSubnets := manager.RosaSubnets{}
	go manageRosaSubnetList(opt.rosaSubnetListPath, &rosaSubnets)

	rosaOidcConfigId, err := os.ReadFile(opt.rosaOIDCConfigId)
	if err != nil {
		klog.Errorf("Failed to read %s: %v", opt.rosaOIDCConfigId, err)
	}

	rosaBillingAccount, err := os.ReadFile(opt.rosaBillingAccount)
	if err != nil {
		klog.Errorf("Failed to read %s: %v", opt.rosaBillingAccount, err)
	}

	ctx := context.Background()
	prowjobInformerFactory.Start(ctx.Done())

	jobManager := manager.NewJobManager(
		configAgent,
		resolver,
		prowClient.ProwV1(),
		prowjobInformer,
		imageClient,
		buildClusterClientConfigs,
		ghClient,
		opt.ForcePROwner,
		&workflows,
		opt.leaseClient,
		hiveConfigMapClient,
		rosaClient,
		rosaSecretClient,
		&rosaSubnets,
		opt.rosaClusterLimit,
		opt.rosaClusterAdminUsername,
		clusterBotMetrics.ErrorRate,
		strings.ReplaceAll(string(rosaOidcConfigId), "\n", ""),
		strings.ReplaceAll(string(rosaBillingAccount), "\n", ""),
	)

	klog.Infof("Waiting for caches to sync")
	cache.WaitForCacheSync(ctx.Done(), hasSynced...)

	if err := jobManager.Start(); err != nil {
		return fmt.Errorf("unable to load initial configuration: %w", err)
	}

	bot := slack.NewBot(botToken, botSigningSecret, opt.GracePeriod, opt.Port, &workflows)
	jiraclient, err := opt.jiraOptions.Client()
	httpClient := &http.Client{Timeout: 60 * time.Second}
	if err != nil {
		klog.Errorf("Failed to load the Jira Client: %s", err)
		Start(bot, nil, jobManager, nil, health, opt.InstrumentationOptions, clusterBotMetrics)
	} else {
		Start(bot, jiraclient.JiraClient(), jobManager, httpClient, health, opt.InstrumentationOptions, clusterBotMetrics)
	}

	return err
}

func processKubeConfigs(kubeConfigs map[string]rest.Config) (utils.BuildClusterClientConfigMap, error) {
	clusterMap := make(utils.BuildClusterClientConfigMap)
	for clusterName, clusterConfig := range kubeConfigs {
		tmpClusterConfig := clusterConfig
		coreClient, err := clientset.NewForConfig(&tmpClusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create core client: %w", err)
		}
		targetImageClient, err := imageclientset.NewForConfig(&tmpClusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create target image client: %w", err)
		}
		projectClient, err := projectclientset.NewForConfig(&tmpClusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create project client: %w", err)
		}
		buildConfigClient, err := buildconfigclientset.NewForConfig(&tmpClusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create project client: %w", err)
		}
		clusterMap[clusterName] = &utils.BuildClusterClientConfig{
			CoreConfig:        &tmpClusterConfig,
			CoreClient:        coreClient,
			ProjectClient:     projectClient,
			TargetImageClient: targetImageClient,
			BuildConfigClient: buildConfigClient,
		}
	}
	return clusterMap, nil
}

func manageRosaSubnetList(path string, subnetList *manager.RosaSubnets) {
	for {
		subnetsRaw, err := os.ReadFile(path)
		if err != nil {
			klog.Errorf("Failed to read %s: %v", path, err)
		}
		newSubnets := sets.New[string](strings.Split(string(subnetsRaw), ",")...)
		subnetList.Lock.Lock()
		subnetList.Subnets = newSubnets
		subnetList.Lock.Unlock()
		time.Sleep(2 * time.Minute)
	}
}

func manageWorkflowConfig(path string, workflows *manager.WorkflowConfig) {
	for {
		// To prevent the ci-chat-bot from crashlooping due to a bad config change,
		// we will only log that the config is broken and set the workflows to an
		// empty map. To prevent broken configs in the future, a presubmit should
		// be creating for openshift/release that verifies this config.
		var config manager.WorkflowConfig
		rawConfig, err := os.ReadFile(path)
		if err != nil {
			klog.Errorf("Failed to load workflow config file at %s: %v", path, err)
		} else if err := yaml.Unmarshal(rawConfig, &config); err != nil {
			klog.Errorf("Failed to unmarshal workflow config: %v", err)
		}

		workflows.Mutex.Lock()
		if config.Workflows != nil {
			workflows.Workflows = config.Workflows
		} else {
			workflows.Workflows = make(map[string]manager.WorkflowConfigItem)
		}
		workflows.Mutex.Unlock()
		time.Sleep(2 * time.Minute)
	}
}

func loadLeaseCredentials(leaseServerCredentialsFile string) (string, func() []byte, error) {
	if err := secret.Add(leaseServerCredentialsFile); err != nil {
		return "", nil, fmt.Errorf("failed to start secret agent on file %s: %s", leaseServerCredentialsFile, string(secret.Censor([]byte(err.Error()))))
	}
	splits := strings.Split(string(secret.GetSecret(leaseServerCredentialsFile)), ":")
	if len(splits) != 2 {
		return "", nil, fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'")
	}
	username := splits[0]
	passwordGetter := func() []byte {
		return []byte(splits[1])
	}
	return username, passwordGetter, nil
}

func (o *options) initializeLeaseClient() error {
	var err error
	username, passwordGetter, err := loadLeaseCredentials(o.leaseServerCredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to load lease credentials: %w", err)
	}

	if o.leaseClient, err = lease.NewClient("ci-chat-bot", o.leaseServer, username, passwordGetter, 60, 0); err != nil {
		return fmt.Errorf("failed to create the lease client: %w", err)
	}
	return nil
}
