package main

import (
	"flag"
	"fmt"
	"gopkg.in/fsnotify.v1"
	"k8s.io/test-infra/prow/interrupts"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	prowapiv1 "github.com/openshift/ci-chat-bot/pkg/prow/apiv1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"
)

type options struct {
	ProwConfigPath           string
	JobConfigPath            string
	GithubEndpoint           string
	ForcePROwner             string
	BuildClusterKubeconfig   string
	ReleaseClusterKubeconfig string
	ConfigResolver           string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// prow registers this on init
	interrupts.OnInterrupt(func() { os.Exit(0) })

	emptyFlags := flag.NewFlagSet("empty", flag.ContinueOnError)
	klog.InitFlags(emptyFlags)
	opt := &options{
		GithubEndpoint: "https://api.github.com",
		ConfigResolver: "http://ci-operator-configresolver.ci.svc/config",
	}
	pflag.StringVar(&opt.ConfigResolver, "config-resolver", opt.ConfigResolver, "A URL pointing to a config resolver for retrieving ci-operator config. You may pass a location on disk with file://<abs_path_to_ci_operator_config>")
	pflag.StringVar(&opt.ProwConfigPath, "prow-config", opt.ProwConfigPath, "A config file containing the prow configuration.")
	pflag.StringVar(&opt.JobConfigPath, "job-config", opt.JobConfigPath, "A config file containing the jobs to run against releases.")
	pflag.StringVar(&opt.GithubEndpoint, "github-endpoint", opt.GithubEndpoint, "An optional proxy for connecting to github.")
	pflag.StringVar(&opt.ForcePROwner, "force-pr-owner", opt.ForcePROwner, "Make the supplied user the owner of all PRs for access control purposes.")
	pflag.StringVar(&opt.BuildClusterKubeconfig, "build-cluster-kubeconfig", "", "Kubeconfig to use for buildcluster. Defaults to normal kubeconfig if unset.")
	pflag.StringVar(&opt.ReleaseClusterKubeconfig, "release-cluster-kubeconfig", "", "Kubeconfig to use for cluster housing the release imagestreams. Defaults to normal kubeconfig if unset.")
	pflag.CommandLine.AddGoFlag(emptyFlags.Lookup("v"))
	pflag.Parse()
	klog.SetOutput(os.Stderr)

	if err := setupKubeconfigWatches(opt); err != nil {
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

	prowJobKubeconfig, _, _, err := loadKubeconfig()
	if err != nil {
		return err
	}
	config, err := loadKubeconfigFromFlagOrDefault(opt.BuildClusterKubeconfig, prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load prowjob kubeconfig: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(prowJobKubeconfig)
	if err != nil {
		return fmt.Errorf("unable to create prow client: %v", err)
	}
	prowClient := dynamicClient.Resource(schema.GroupVersionResource{Group: "prow.k8s.io", Version: "v1", Resource: "prowjobs"})
	client, err := clientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to create client: %v", err)
	}
	// Config and Client to access release images
	releaseConfig, err := loadKubeconfigFromFlagOrDefault(opt.ReleaseClusterKubeconfig, prowJobKubeconfig)
	imageClient, err := imageclientset.NewForConfig(releaseConfig)
	if err != nil {
		return fmt.Errorf("unable to create image client: %v", err)
	}
	projectClient, err := projectclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to create project client: %v", err)
	}

	configAgent := &prowapiv1.Agent{}
	if err := configAgent.Start(opt.ProwConfigPath, opt.JobConfigPath); err != nil {
		return err
	}

	manager := NewJobManager(configAgent, resolver, prowClient, client, imageClient, projectClient, config, opt.GithubEndpoint, opt.ForcePROwner)
	if err := manager.Start(); err != nil {
		return fmt.Errorf("unable to load initial configuration: %v", err)
	}

	bot := NewBot(botToken)
	for {
		if err := bot.Start(manager); err != nil && !isRetriable(err) {
			return err
		}
		time.Sleep(5 * time.Second)
	}
}

func setupKubeconfigWatches(o *options) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to set up watcher: %w", err)
	}
	for _, candidate := range []string{o.BuildClusterKubeconfig, o.ReleaseClusterKubeconfig, "/var/run/secrets/kubernetes.io/serviceaccount/token"} {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if err := watcher.Add(candidate); err != nil {
			return fmt.Errorf("failed to watch %s: %w", candidate, err)
		}
	}

	go func() {
		for e := range watcher.Events {
			if e.Op == fsnotify.Chmod {
				// For some reason we get frequent chmod events from Openshift
				continue
			}
			klog.Infof("event: %s, kubeconfig changed, exiting to make the kubelet restart us so we can pick them up", e.String())
			interrupts.Terminate()
		}
	}()

	return nil
}