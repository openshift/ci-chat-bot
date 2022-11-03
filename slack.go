package main

import (
	"encoding/json"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack"
	eventhandler "github.com/openshift/ci-chat-bot/pkg/slack/events"
	eventrouter "github.com/openshift/ci-chat-bot/pkg/slack/events/router"
	"github.com/sirupsen/logrus"
	slackClient "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/klog"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/simplifypath"
	"net/http"
	"strconv"
	"time"
)

const (
	MetricsPort           = 9090
	PProfPort             = 6060
	HealthPort            = 8081
	MemoryProfileInterval = 30 * time.Second
)

var (
	promMetrics = metrics.NewMetrics("cluster_bot")
)

func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func Start(bot *slack.Bot, jobManager manager.JobManager) {
	slackClient := slackClient.New(bot.BotToken)
	jobManager.SetNotifier(bot.JobResponder(slackClient))

	metrics.ExposeMetrics("ci-chat-bot", config.PushGateway{}, MetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicking the root
		l(""), // for black-box health checks
		l("slack",
			l("events-endpoint"),
		),
	))
	handler := metrics.TraceHandler(simplifier, promMetrics.HTTPRequestDuration, promMetrics.HTTPResponseSize)
	pprof.Instrument(prowflagutil.InstrumentationOptions{
		MetricsPort:           MetricsPort,
		PProfPort:             PProfPort,
		HealthPort:            HealthPort,
		MemoryProfileInterval: MemoryProfileInterval,
	})
	health := pjutil.NewHealth()

	mux := http.NewServeMux()
	// handle the root to allow for a simple uptime probe
	mux.Handle("/", handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusOK) })))
	mux.Handle("/slack/events-endpoint", handler(handleEvent(bot.BotSigningSecret, eventrouter.ForEvents(slackClient, jobManager, bot.SupportedCommands()))))
	server := &http.Server{Addr: ":" + strconv.Itoa(bot.Port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	health.ServeReady()

	interrupts.ListenAndServe(server, bot.GracePeriod)
	interrupts.WaitForGracefulShutdown()

	klog.Infof("ci-chat-bot up and listening to slack")
}

func handleEvent(signingSecret string, handler eventhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "events")
		body, ok := slack.VerifiedBody(request, signingSecret)
		if !ok {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		event, err := slackevents.ParseEvent(body, slackevents.OptionNoVerifyToken())
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		if event.Type == slackevents.URLVerification {
			var response *slackevents.ChallengeResponse
			err := json.Unmarshal(body, &response)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text")
			if _, err := writer.Write([]byte(response.Challenge)); err != nil {
				klog.Errorf("Failed to write response. %v", err)
			}
		}

		// we always want to respond with 200 immediately
		writer.WriteHeader(http.StatusOK)
		// we don't really care how long this takes
		go func() {
			if err := handler.Handle(&event, logger); err != nil {
				klog.Errorf("Failed to handle event: %v", err)
			}
		}()
	}
}
