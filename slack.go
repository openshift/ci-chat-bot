package main

import (
	"encoding/json"
	jiraClient "github.com/andygrunwald/go-jira"
	"github.com/openshift/ci-chat-bot/pkg/jira"
	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack"
	eventhandler "github.com/openshift/ci-chat-bot/pkg/slack/events"
	eventrouter "github.com/openshift/ci-chat-bot/pkg/slack/events/router"
	interactionhandler "github.com/openshift/ci-chat-bot/pkg/slack/interactions"
	interactionrouter "github.com/openshift/ci-chat-bot/pkg/slack/interactions/router"
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

func Start(bot *slack.Bot, jiraclient *jiraClient.Client, jobManager manager.JobManager) {
	slackclient := slackClient.New(bot.BotToken)
	jobManager.SetNotifier(bot.JobResponder(slackclient))
	var issueFiler jira.IssueFiler
	if jiraclient != nil {
		var err error
		issueFiler, err = jira.NewIssueFiler(slackclient, jiraclient)
		if err != nil {
			klog.Errorf(" Could not initialize Jira issue filer: %s", err)
		}
	} else {
		issueFiler = nil
	}

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
	mux.Handle("/slack/events-endpoint", handler(handleEvent(bot.BotSigningSecret, eventrouter.ForEvents(slackclient, jobManager, bot.SupportedCommands()))))
	mux.Handle("/slack/interactive-endpoint", handler(handleInteraction(bot.BotSigningSecret, interactionrouter.ForModals(issueFiler, slackclient))))
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

func handleInteraction(signingSecret string, handler interactionhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "interactionhandler")
		if _, ok := slack.VerifiedBody(request, signingSecret); !ok {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		var callback slackClient.InteractionCallback
		payload := request.FormValue("payload")
		if err := json.Unmarshal([]byte(payload), &callback); err != nil {
			logger.WithError(err).WithField("payload", payload).Error("Failed to unmarshal an interaction payload.")
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		logger.WithField("interaction", callback).Trace("Read an interaction payload.")
		logger = logger.WithFields(fieldsFor(&callback))
		response, err := handler.Handle(&callback, logger)
		if err != nil {
			logger.WithError(err).Error("Failed to handle interaction payload.")
		}
		if len(response) == 0 {
			writer.WriteHeader(http.StatusOK)
			return
		}
		logger.WithField("body", string(response)).Trace("Sending interaction payload response.")
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Length", strconv.Itoa(len(response)))
		if _, err := writer.Write(response); err != nil {
			logger.WithError(err).Error("Failed to send interaction payload response.")
		}
	}
}

func fieldsFor(interactionCallback *slackClient.InteractionCallback) logrus.Fields {
	return logrus.Fields{
		"trigger_id":  interactionCallback.TriggerID,
		"callback_id": interactionCallback.CallbackID,
		"action_id":   interactionCallback.ActionID,
		"type":        interactionCallback.Type,
	}
}
