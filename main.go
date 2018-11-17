package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/prow"
	"k8s.io/client-go/dynamic"

	"github.com/sbstjn/hanu"
)

func main() {
	slack, err := hanu.New(os.Getenv("BOT_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}

	Version := "0.0.1"

	q := newWorkQueue()

	go q.start()

	slack.Commands = append(slack.Commands,

		hanu.NewCommand(
			"launch <image:string>",
			"Launch an OpenShift cluster using the provided release image. Pass `latest` to use the most recent cluster version. Send this as a direct message. You will receive a response when the cluster is up for the credentials of the KUBECONFIG file.",
			func(conv hanu.ConversationInterface) {
				if !conv.Message().IsDirectMessage() {
					conv.Reply("you must direct message me the launch request")
					return
				}
				user := conv.Message().User()
				image, _ := conv.String("image")
				if len(image) == 0 || image == "latest" {
					image = "registry.svc.ci.openshift.org/openshift/origin-release:v4.0"
				}
				response := q.requestCluster(user, image, func(cluster cluster) {
					conv.Reply("your cluster is now available, credentials are:\n\n%s", cluster.credentials)
				})
				conv.Reply(response)
			},
		),
		hanu.NewCommand(
			"list",
			"See who is hogging all the clusters.",
			func(conv hanu.ConversationInterface) {
				conv.Reply(q.listClusters())
			},
		),

		hanu.NewCommand(
			"auth",
			"Send the credentials for the cluster you most recently requested",
			func(conv hanu.ConversationInterface) {
				if !conv.Message().IsDirectMessage() {
					conv.Reply("you must direct message me this request")
					return
				}
				user := conv.Message().User()
				conv.Reply(q.requestCredentials(user))
			},
		),
		hanu.NewCommand(
			"version",
			"Report the version of the bot",
			func(conv hanu.ConversationInterface) {
				conv.Reply("Thanks for asking! I'm running `%s`", Version)
			},
		),
	)

	slack.Listen()
}

type cluster struct {
	name string

	releaseImage string

	credentials string
	failure     string

	requestedBy string
	requestedAt time.Time
	expiresAt   time.Time
}

func (c *cluster) State() string {
	if len(c.credentials) > 0 {
		return "available"
	}
	return "pending"
}

type userRequest struct {
	user         string
	releaseImage string
	response     callbackFunc

	requestedAt time.Time
	cluster     string
}

type workQueue struct {
	lock         sync.Mutex
	requests     map[string]*userRequest
	clusters     map[string]*cluster
	waitList     []string
	lastEstimate time.Duration

	prefix      string
	maxClusters int
	maxAge      time.Duration

	prowConfigLoader ProwConfigLoader
	prowClient       dynamic.ResourceInterface
	coreClient coreclient.Interface
	prowJobName string
}

func newWorkQueue() *workQueue {
	return &workQueue{
		requests:     make(map[string]*userRequest),
		clusters:     make(map[string]*cluster),
		prefix:       "chat-bot-",
		maxClusters:  5,
		maxAge:       1 * time.Minute, // 2 * time.Hour,
		lastEstimate: 10 * time.Minute,
	}
}

func (q *workQueue) start() {
	for {
		q.clean()
		time.Sleep(20 * time.Second)
	}
}

func (q *workQueue) clean() {
	q.lock.Lock()
	defer q.lock.Unlock()

	// forget everything that is too old
	now := time.Now()
	for _, cluster := range q.clusters {
		if cluster.expiresAt.Before(now) {
			log.Printf("cluster %q is expired", cluster.name)
			delete(q.clusters, cluster.name)
		}
	}
	for _, req := range q.requests {
		if req.requestedAt.Add(q.maxAge * 2).Before(now) {
			log.Printf("request %q is expired", req.user)
			delete(q.requests, req.user)
		}
	}
	remaining := make([]string, 0, len(q.waitList))
	for _, user := range q.waitList {
		if _, ok := q.requests[user]; !ok {
			continue
		}
		remaining = append(remaining, user)
	}
	q.waitList = remaining
}

func (q *workQueue) estimateCompletion(req *userRequest) string {
	if req == nil {
		return q.lastEstimate.Truncate(time.Second).String()
	}
	lastEstimate := q.lastEstimate - time.Now().Sub(req.requestedAt)
	if lastEstimate < 0 {
		return "soon"
	}
	return lastEstimate.Truncate(time.Second).String()
}

func (q *workQueue) listClusters() string {
	q.lock.Lock()
	defer q.lock.Unlock()

	var clusters []*cluster
	for _, cluster := range q.clusters {
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].requestedAt.Before(clusters[j].requestedAt) {
			return true
		}
		if clusters[i].name < clusters[j].name {
			return true
		}
		return false
	})
	buf := &bytes.Buffer{}
	now := time.Now()
	if len(clusters) == 0 {
		fmt.Fprintf(buf, "No clusters have been requested")
	} else {
		fmt.Fprintf(buf, "%d/%d clusters created:\n\n", len(clusters), q.maxClusters)
		for _, cluster := range clusters {
			fmt.Fprintf(buf, "* `%s` %d minutes ago by <@%s> - %s\n", cluster.name, int(now.Sub(cluster.requestedAt)/time.Minute), cluster.requestedBy, cluster.State())
		}
		fmt.Fprintf(buf, "\n")
	}
	if len(q.waitList) > 0 {
		fmt.Fprintf(buf, "%d people on the waitlist\n", len(q.waitList), strings.Join(q.waitList, ", "))
	}
	return buf.String()
}

type callbackFunc func(cluster cluster)

func (q *workQueue) requestCredentials(user string) string {
	q.lock.Lock()
	defer q.lock.Unlock()

	existing, ok := q.requests[user]
	if !ok {
		return fmt.Sprintf("you haven't requested a cluster or your cluster expired")
	}
	if len(existing.cluster) == 0 {
		return fmt.Sprintf("you are still on the waitlist")
	}
	cluster, ok := q.clusters[existing.cluster]
	if !ok {
		return fmt.Sprintf("your cluster has expired and credentials are no longer available")
	}
	if len(cluster.credentials) == 0 {
		return fmt.Sprintf("the cluster is still starting and should be ready in ~%s", q.estimateCompletion(existing))
	}
	return fmt.Sprintf("your credentials are:\n\n%s", cluster.credentials)
}

func (q *workQueue) requestCluster(user string, releaseImage string, fn callbackFunc) string {
	q.lock.Lock()
	defer q.lock.Unlock()

	existing, ok := q.requests[user]
	if ok {
		if len(existing.cluster) == 0 {
			log.Printf("user %q already requested cluster", user)
			return fmt.Sprintf("you have already requested a cluster and it should be ready in ~%s", q.estimateCompletion(existing))
		}
		if cluster, ok := q.clusters[existing.cluster]; ok {
			if len(cluster.credentials) > 0 {
				log.Printf("user %q cluster is already up", user)
				return fmt.Sprintf("your cluster is already running, see your credentials again with the 'auth' command")
			}
			log.Printf("user %q cluster has no credentials yet", user)
			return fmt.Sprintf("you have already requested a cluster and it should be ready in ~%s", q.estimateCompletion(existing))
		}
	}
	req := &userRequest{
		user:         user,
		releaseImage: releaseImage,
		requestedAt:  time.Now(),
		response:     fn,
	}
	q.requests[user] = req

	if len(q.clusters) >= q.maxClusters {
		log.Printf("user %q is waitlisted", user)
		q.waitList = append(q.waitList, user)
		return fmt.Sprintf("no clusters are currently available, I'll msg you when one frees up")
	}

	newCluster := &cluster{
		name:         fmt.Sprintf("%s%s", q.prefix, req.requestedAt.UTC().Format("2006-01-02-030405.9999")),
		requestedBy:  user,
		requestedAt:  req.requestedAt,
		releaseImage: req.releaseImage,
		expiresAt:    req.requestedAt.Add(q.maxAge),
	}
	req.cluster = newCluster.name
	q.clusters[newCluster.name] = newCluster

	log.Printf("user %q requests cluster %q", user, newCluster.name)
	go q.track(*newCluster)

	return fmt.Sprintf("your cluster will be `%s` and should be ready in ~%s", newCluster.name, q.estimateCompletion(req))
}

func (q *workQueue) clusterFinish(cluster cluster) {
	q.lock.Lock()
	defer q.lock.Unlock()

	now := time.Now()
	q.lastEstimate = now.Sub(cluster.requestedAt)

	q.clusters[cluster.name] = &cluster
	for _, request := range q.requests {
		if request.cluster == cluster.name {
			log.Printf("notify %q that cluster %q is complete", request.user, request.cluster)
			if request.response != nil {
				go request.response(cluster)
			}
		}
	}
}

func (q *workQueue) track(cluster cluster) {
	if err := q.launch(&cluster); err != nil {
		cluster.failure = err.Error()
	}
	q.clusterFinish(cluster)
}

func (q *workQueue) launch(cluster *cluster) error {
	job, err := prow.JobForConfig(q.prowConfigLoader, q.prowJobName)
	if err != nil {
		return err
	}
	job.ObjectMeta = metav1.ObjectMeta{
		Name: cluster.name,
		Annotations: map[string]string{
			"ci-chat-bot.openshift.io/user": cluster.requestedBy,

			"prow.k8s.io/job": spec.Job,
		},
		Labels: map[string]string{
			"ci-chat-bot.openshift.io/launch": "true",

			"prow.k8s.io/type": string(spec.Type),
			"prow.k8s.io/job":  spec.Job,
		},
	}
	prow.OverrideJobEnvironment(job, cluster.releaseImage)
	out, err := q.prowClient.Create(prow.ObjectToUnstructured(pj))
	if err != nil {
		if !errors.IsAlreadyExist(err) {
			return err
		}
	}

	seen := false
	err = wait.Until(5 * time.Second, 10 * time.Minute, func() (bool, error) {
		pod , err := q.coreClient.Pods(q.prowNamespace).Get(cluster.name, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				return false, err			
			}
			if seen {
				return false, fmt.Errorf("pod was deleted")
			}
			seen = true
			continue
		}
		if pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed" {
			return false, fmt.Errorf("pod has already exited")
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("unable to check launch status: %v", err)
	}

	// launch a prow job, tied back to this cluster user

	// wait for job to create namespace

	// wait for launch pod to start running

	// periodically poll for the config file

	// try to connect to the cluster with credentials

	// when they succeed, store them on the cluster object and notify the human

	time.Sleep(3 * time.Second)
	cluster.credentials = "```\nsome auth info\n```"
	return nil
}
