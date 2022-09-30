package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"log"
	"net/url"
	"os"
	"path"
	"regexp"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"gopkg.in/fsnotify.v1"
	"gopkg.in/yaml.v2"

	"github.com/spf13/pflag"

	citools "github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"

	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"
)

var (
	// TODO: I'm leaving this in place to allow the clusterbot to keep working while we switch over to token files. This can be removed once we switch over.
	reBuildClusterName = regexp.MustCompile(`^(.*).kubeconfig$`)

	reBuildClusterKubeconfig = regexp.MustCompile(`^sa.ci-chat-bot.(.*).config$`)
	reBuildClusterTokenFile  = regexp.MustCompile(`^sa.ci-chat-bot.(.*).token$`)
)

type options struct {
	prowconfig                      configflagutil.ConfigOptions
	GitHubOptions                   flagutil.GitHubOptions
	ForcePROwner                    string
	BuildClusterKubeconfigsLocation string
	ReleaseClusterKubeconfig        string
	ConfigResolver                  string
	WorkflowConfigPath              string
	Port                            int
	GracePeriod                     time.Duration
}

func (o *options) Validate() error {
	if o.ReleaseClusterKubeconfig != "" {
		if _, err := os.Stat(o.ReleaseClusterKubeconfig); err != nil {
			return fmt.Errorf("error accessing --release-cluster-kubeconfig: %w", err)
		}
	}
	if o.BuildClusterKubeconfigsLocation != "" {
		if fileInfo, err := os.Stat(o.BuildClusterKubeconfigsLocation); err != nil {
			return fmt.Errorf("error accessing --build-cluster-kubeconfigs-location: %w", err)
		} else if !fileInfo.IsDir() {
			return fmt.Errorf("--build-cluster-kubeconfigs-location must be a directory")
		}
	}
	return o.GitHubOptions.Validate(false)
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
		ConfigResolver:                  "http://config.ci.openshift.org/config",
		BuildClusterKubeconfigsLocation: "/var/build-cluster-kubeconfigs",
	}

	// TODO: After removing this option in the deployment, it will be safe to remove these...
	var ignored string
	pflag.StringVar(&ignored, "build-cluster-kubeconfig", ignored, "REMOVED: Kubeconfig to use for buildcluster. Defaults to normal kubeconfig if unset.")

	pflag.StringVar(&opt.ConfigResolver, "config-resolver", opt.ConfigResolver, "A URL pointing to a config resolver for retrieving ci-operator config. You may pass a location on disk with file://<abs_path_to_ci_operator_config>")
	pflag.StringVar(&opt.ForcePROwner, "force-pr-owner", opt.ForcePROwner, "Make the supplied user the owner of all PRs for access control purposes.")
	pflag.StringVar(&opt.BuildClusterKubeconfigsLocation, "build-cluster-kubeconfigs-location", opt.BuildClusterKubeconfigsLocation, "Path to the location of the Kubeconfigs for the various buildclusters. Default is \"/var/build-cluster-kubeconfigs\".")
	pflag.StringVar(&opt.ReleaseClusterKubeconfig, "release-cluster-kubeconfig", "", "Kubeconfig to use for cluster housing the release imagestreams. Defaults to normal kubeconfig if unset.")
	pflag.StringVar(&opt.WorkflowConfigPath, "workflow-config-path", "", "Path to config file used for workflow commands")
	pflag.IntVar(&opt.Port, "port", 8080, "Port to listen on.")
	pflag.DurationVar(&opt.GracePeriod, "grace-period", 5*time.Second, "On shutdown, try to handle remaining events for the specified duration.")

	opt.prowconfig.AddFlags(emptyFlags)
	opt.GitHubOptions.AddFlags(emptyFlags)
	pflag.CommandLine.AddGoFlagSet(emptyFlags)
	pflag.Parse()
	klog.SetOutput(os.Stderr)

	if err := opt.Validate(); err != nil {
		return fmt.Errorf("unable to validate program arguments: %v", err)
	}

	buildClusterClientConfigs, err := readBuildClusterKubeConfigs(opt.BuildClusterKubeconfigsLocation)
	if err != nil {
		return fmt.Errorf("unable to load build cluster configurations: %v", err)
	}

	if err := setupKubeconfigWatches(buildClusterClientConfigs); err != nil {
		klog.Warningf("failed to set up kubeconfig watches: %v", err)
	}

	resolverURL, err := url.Parse(opt.ConfigResolver)
	if err != nil {
		return fmt.Errorf("--config-resolver is not a valid URL: %v", err)
	}
	resolver := &URLConfigResolver{URL: resolverURL}

	botToken := os.Getenv("BOT_TOKEN")
	if len(botToken) == 0 {
		return fmt.Errorf("the environment variable BOT_TOKEN must be set")
	}

	botSigningSecret := os.Getenv("BOT_SIGNING_SECRET")
	if len(botSigningSecret) == 0 {
		return fmt.Errorf("the environment variable BOT_SIGNING_SECRET must be set")
	}

	prowJobKubeconfig, _, _, err := loadKubeconfig()
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to create prow client: %v", err)
	}
	prowClient := dynamicClient.Resource(schema.GroupVersionResource{Group: "prow.k8s.io", Version: "v1", Resource: "prowjobs"})

	// Config and Client to access release images
	releaseConfig, err := loadKubeconfigFromFlagOrDefault(opt.ReleaseClusterKubeconfig, prowJobKubeconfig)
	imageClient, err := imageclientset.NewForConfig(releaseConfig)
	if err != nil {
		return fmt.Errorf("unable to create image client: %v", err)
	}

	configAgent, err := opt.prowconfig.ConfigAgent()
	if err != nil {
		return err
	}

	workflows := WorkflowConfig{}
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
			return fmt.Errorf("unable to create github client: %v", err)
		}
	}

	manager := NewJobManager(configAgent, resolver, prowClient, imageClient, buildClusterClientConfigs, ghClient, opt.ForcePROwner, &workflows)
	if err := manager.Start(); err != nil {
		return fmt.Errorf("unable to load initial configuration: %v", err)
	}

	bot := NewBot(botToken, botSigningSecret, opt.GracePeriod, opt.Port, &workflows)
	bot.Start(manager)

	return err
}

type BuildClusterClientConfig struct {
	KubeconfigPath    string
	TokenPath         string
	CoreConfig        *rest.Config
	CoreClient        *clientset.Clientset
	ProjectClient     *projectclientset.Clientset
	TargetImageClient *imageclientset.Clientset
}

type BuildClusterClientConfigMap map[string]*BuildClusterClientConfig

func readBuildClusterKubeConfigs(location string) (BuildClusterClientConfigMap, error) {
	files, err := ioutil.ReadDir(location)
	if err != nil {
		return nil, fmt.Errorf("unable to access location %q: %v", location, err)
	}
	clusterMap := make(BuildClusterClientConfigMap)
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		var clusterName string
		fullPath := path.Join(location, file.Name())

		if m := reBuildClusterName.FindStringSubmatch(file.Name()); m != nil {
			clusterName = m[1]
		} else if m := reBuildClusterKubeconfig.FindStringSubmatch(file.Name()); m != nil {
			clusterName = m[1]
		} else if m := reBuildClusterTokenFile.FindStringSubmatch(file.Name()); m != nil {
			clusterName = m[1]
			if cm, ok := clusterMap[clusterName]; !ok {
				clusterMap[clusterName] = &BuildClusterClientConfig{
					TokenPath: fullPath,
				}
			} else {
				cm.TokenPath = fullPath
			}
			continue
		} else {
			klog.Warningf("Unrecognized file name: %q", file.Name())
			continue
		}

		contents, err := ioutil.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("unable to access kubeconfig %q: %v", file.Name(), err)
		}
		cfg, err := clientcmd.NewClientConfigFromBytes(contents)
		if err != nil {
			return nil, fmt.Errorf("could not load build client configuration: %v", err)
		}
		clusterConfig, err := cfg.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("could not load cluster configuration: %v", err)
		}
		coreClient, err := clientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create core client: %v", err)
		}
		targetImageClient, err := imageclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create target image client: %v", err)
		}
		projectClient, err := projectclientset.NewForConfig(clusterConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to create project client: %v", err)
		}
		if cm, ok := clusterMap[clusterName]; !ok {
			clusterMap[clusterName] = &BuildClusterClientConfig{
				KubeconfigPath:    fullPath,
				CoreConfig:        clusterConfig,
				CoreClient:        coreClient,
				ProjectClient:     projectClient,
				TargetImageClient: targetImageClient,
			}
		} else {
			cm.KubeconfigPath = fullPath
			cm.CoreConfig = clusterConfig
			cm.CoreClient = coreClient
			cm.ProjectClient = projectClient
			cm.TargetImageClient = targetImageClient
		}
	}
	return clusterMap, nil
}

func setupKubeconfigWatches(clusters BuildClusterClientConfigMap) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to set up watcher: %w", err)
	}
	for _, cluster := range clusters {
		watchFile := cluster.KubeconfigPath
		if len(cluster.TokenPath) > 0 {
			watchFile = cluster.TokenPath
		}
		if _, err := os.Stat(watchFile); err != nil {
			continue
		}
		if err := watcher.Add(watchFile); err != nil {
			return fmt.Errorf("failed to watch %v: %w", cluster, err)
		}
	}

	go func() {
		for e := range watcher.Events {
			if e.Op == fsnotify.Chmod {
				// For some reason we get frequent chmod events from Openshift
				continue
			}
			klog.Infof("event: %s, kubeconfig changed, exiting to make the kubelet restart us so we can pick them up", e.String())
			os.Exit(0)
		}
	}()

	return nil
}

type WorkflowConfig struct {
	Workflows map[string]WorkflowConfigItem `yaml:"workflows"`
	mutex     sync.RWMutex                  `yaml:"-"` // this field just allows us to update the above values without races
}

type WorkflowConfigItem struct {
	BaseImages   map[string]citools.ImageStreamTagReference `yaml:"base_images,omitempty"`
	Architecture string                                     `yaml:"architecture,omitempty"`
	Platform     string                                     `yaml:"platform"`
}

func manageWorkflowConfig(path string, workflows *WorkflowConfig) {
	for {
		// To prevent the ci-chat-bot from crashlooping due to a bad config change,
		// we will only log that the config is broken and set the workflows to an
		// empty map. To prevent broken configs in the future, a presubmit should
		// be creating for openshift/release that verifies this config.
		var config WorkflowConfig
		rawConfig, err := ioutil.ReadFile(path)
		if err != nil {
			klog.Errorf("Failed to load workflow config file at %s: %v", path, err)
		} else if err := yaml.Unmarshal(rawConfig, &config); err != nil {
			klog.Errorf("Failed to unmarshal workflow config: %v", err)
		}

		workflows.mutex.Lock()
		if config.Workflows != nil {
			workflows.Workflows = config.Workflows
		} else {
			workflows.Workflows = make(map[string]WorkflowConfigItem)
		}
		workflows.mutex.Unlock()
		time.Sleep(2 * time.Minute)
	}
}
