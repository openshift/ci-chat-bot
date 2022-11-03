package main

import (
	"flag"
	"fmt"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"

	"k8s.io/client-go/rest"
	"k8s.io/test-infra/pkg/flagutil"

	"gopkg.in/yaml.v2"

	"github.com/spf13/pflag"

	citools "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"
)

type options struct {
	prowconfig               configflagutil.ConfigOptions
	GitHubOptions            prowflagutil.GitHubOptions
	KubernetesOptions        prowflagutil.KubernetesOptions
	ForcePROwner             string
	ReleaseClusterKubeconfig string
	ConfigResolver           string
	WorkflowConfigPath       string
	Port                     int
	GracePeriod              time.Duration

	leaseServer                string
	leaseServerCredentialsFile string
	leaseClient                manager.LeaseClient

	overrideLaunchLabel string

	jiraOptions prowflagutil.JiraOptions
}

func (o *options) Validate() error {
	if o.ReleaseClusterKubeconfig != "" {
		if _, err := os.Stat(o.ReleaseClusterKubeconfig); err != nil {
			return fmt.Errorf("error accessing --release-cluster-kubeconfig: %w", err)
		}
	}
	for _, group := range []flagutil.OptionGroup{&o.GitHubOptions, &o.KubernetesOptions} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}
	return nil
}

func main() {
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
		ConfigResolver:    "http://config.ci.openshift.org/config",
		KubernetesOptions: prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true},
	}

	pflag.StringVar(&opt.ConfigResolver, "config-resolver", opt.ConfigResolver, "A URL pointing to a config resolver for retrieving ci-operator config. You may pass a location on disk with file://<abs_path_to_ci_operator_config>")
	pflag.StringVar(&opt.ForcePROwner, "force-pr-owner", opt.ForcePROwner, "Make the supplied user the owner of all PRs for access control purposes.")
	pflag.StringVar(&opt.ReleaseClusterKubeconfig, "release-cluster-kubeconfig", "", "Kubeconfig to use for cluster housing the release imagestreams. Defaults to normal kubeconfig if unset.")
	pflag.StringVar(&opt.WorkflowConfigPath, "workflow-config-path", "", "Path to config file used for workflow commands")
	pflag.IntVar(&opt.Port, "port", 8080, "Port to listen on.")
	pflag.DurationVar(&opt.GracePeriod, "grace-period", 5*time.Second, "On shutdown, try to handle remaining events for the specified duration.")
	pflag.StringVar(&opt.leaseServer, "lease-server", citools.URLForService(citools.ServiceBoskos), "Address of the server that manages leases. Used to identify accounts with more available leases.")
	pflag.StringVar(&opt.leaseServerCredentialsFile, "lease-server-credentials-file", "", "The path to credentials file used to access the lease server. The content is of the form <username>:<password>.")
	pflag.StringVar(&opt.overrideLaunchLabel, "override-launch-label", "", "Override the default launch label for jobs. Used for local debugging.")

	opt.prowconfig.AddFlags(emptyFlags)
	opt.GitHubOptions.AddFlags(emptyFlags)
	opt.KubernetesOptions.AddFlags(emptyFlags)
	opt.jiraOptions.AddFlags(emptyFlags)
	pflag.CommandLine.AddGoFlagSet(emptyFlags)
	pflag.Parse()
	klog.SetOutput(os.Stderr)

	if err := opt.Validate(); err != nil {
		return fmt.Errorf("unable to validate program arguments: %w", err)
	}

	if opt.overrideLaunchLabel != "" {
		utils.LaunchLabel = opt.overrideLaunchLabel
	}

	err := opt.KubernetesOptions.AddKubeconfigChangeCallback(func() {
		klog.Infof("received kubeconfig changed event, exiting to make the kubelet restart us so we can pick them up")
		os.Exit(0)
	})
	if err != nil {
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

	prowJobKubeconfig, _, _, err := utils.LoadKubeconfig()
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to create prow client: %w", err)
	}
	prowClient := dynamicClient.Resource(schema.GroupVersionResource{Group: "prow.k8s.io", Version: "v1", Resource: "prowjobs"})

	// Config and Client to access release images
	releaseConfig, err := utils.LoadKubeconfigFromFlagOrDefault(opt.ReleaseClusterKubeconfig, prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to load kubeConfig from flag or default: %w", err)
	}
	imageClient, err := imageclientset.NewForConfig(releaseConfig)
	if err != nil {
		return fmt.Errorf("unable to create image client: %w", err)
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

	jobManager := manager.NewJobManager(configAgent, resolver, prowClient, imageClient, buildClusterClientConfigs, ghClient, opt.ForcePROwner, &workflows, opt.leaseClient)
	if err := jobManager.Start(); err != nil {
		return fmt.Errorf("unable to load initial configuration: %w", err)
	}

	bot := slack.NewBot(botToken, botSigningSecret, opt.GracePeriod, opt.Port, &workflows)
	jiraclient, err := opt.jiraOptions.Client()
	if err != nil {
		klog.Errorf("Failed to load the Jira Client: %s", err)
		Start(bot, nil, jobManager)
	} else {
		Start(bot, jiraclient.JiraClient(), jobManager)
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
		clusterMap[clusterName] = &utils.BuildClusterClientConfig{
			CoreConfig:        &tmpClusterConfig,
			CoreClient:        coreClient,
			ProjectClient:     projectClient,
			TargetImageClient: targetImageClient,
		}
	}
	return clusterMap, nil
}

func manageWorkflowConfig(path string, workflows *manager.WorkflowConfig) {
	for {
		// To prevent the ci-chat-bot from crashlooping due to a bad config change,
		// we will only log that the config is broken and set the workflows to an
		// empty map. To prevent broken configs in the future, a presubmit should
		// be creating for openshift/release that verifies this config.
		var config manager.WorkflowConfig
		rawConfig, err := ioutil.ReadFile(path)
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
