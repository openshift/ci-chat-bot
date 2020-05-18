package main

import (
	"flag"
	"fmt"
	"log"
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
	ProwConfigPath         string
	JobConfigPath          string
	GithubEndpoint         string
	ForcePROwner           string
	BuildClusterKubeconfig string
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
		GithubEndpoint: "https://api.github.com",
	}
	pflag.StringVar(&opt.ProwConfigPath, "prow-config", opt.ProwConfigPath, "A config file containing the prow configuration.")
	pflag.StringVar(&opt.JobConfigPath, "job-config", opt.JobConfigPath, "A config file containing the jobs to run against releases.")
	pflag.StringVar(&opt.GithubEndpoint, "github-endpoint", opt.GithubEndpoint, "An optional proxy for connecting to github.")
	pflag.StringVar(&opt.ForcePROwner, "force-pr-owner", opt.ForcePROwner, "Make the supplied user the owner of all PRs for access control purposes.")
	pflag.StringVar(&opt.BuildClusterKubeconfig, "build-cluster-kubeconfig", "", "Kubeconfig to use for buildcluster. Defaults to normal kubeconfig if unset.")
	pflag.CommandLine.AddGoFlag(emptyFlags.Lookup("v"))
	pflag.Parse()
	klog.SetOutput(os.Stderr)

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
	imageClient, err := imageclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to create client: %v", err)
	}
	projectClient, err := projectclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("unable to create client: %v", err)
	}

	configAgent := &prowapiv1.Agent{}
	if err := configAgent.Start(opt.ProwConfigPath, opt.JobConfigPath); err != nil {
		return err
	}

	manager := NewJobManager(configAgent, prowClient, client, imageClient, projectClient, config, opt.GithubEndpoint, opt.ForcePROwner)
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
