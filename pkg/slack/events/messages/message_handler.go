package messages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/openshift/ci-chat-bot/pkg/slack/events"
	"github.com/openshift/ci-chat-bot/pkg/slack/parser"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
)

func Handle(client *slack.Client, manager manager.JobManager, botCommands []parser.BotCommand) events.PartialHandler {
	return events.PartialHandlerFunc("direct-message",
		func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (handled bool, err error) {
			if callback.Type != slackevents.CallbackEvent {
				return true, nil
			}
			event, ok := callback.InnerEvent.Data.(*slackevents.MessageEvent)
			if !ok {
				return false, fmt.Errorf("failed to parse the slack event")
			}
			mceConfig := manager.GetMceUserConfig()
			mceConfig.Mutex.RLock()
			users := mceConfig.Users
			var allowed bool
			for user := range users {
				if user == event.User {
					allowed = true
					break
				}
			}
			mceConfig.Mutex.RUnlock()
			text := strings.TrimSpace(event.Text)
			if text == "help" || strings.HasPrefix(text, "help ") {
				parts := strings.Split(text, " ")
				if len(parts) == 1 {
					HelpOverview(client, event, botCommands, allowed)
				} else {
					HelpSpecific(client, event, parts[1], botCommands, allowed)
				}
				return true, nil
			}
			// do not respond to bots
			if event.BotID != "" {
				return true, nil
			}
			// do not respond to indirect messages
			if !strings.HasPrefix(event.Channel, "D") {
				_, _, err := client.PostMessage(event.Channel, slack.MsgOptionText("this command is only accepted via direct message)", false))
				if err != nil {
					return false, err
				}
				return true, nil
			}
			// do not respond if the event SubType is message_changed or file_share( in cases a link is posted and a preview is
			// added afterwards and when an attachment is included)
			if event.SubType == "message_changed" || event.SubType == "file_share" {
				return true, nil
			}
			for _, command := range botCommands {
				if command.IsPrivate() && !allowed {
					continue
				}
				properties, match := command.Match(event.Text)
				if match {
					response := command.Execute(client, manager, event, properties)
					if err := postResponse(client, event, response); err != nil {
						return false, fmt.Errorf("failed all attempts to post the response to the requested action: %s", event.Text)
					}
					return true, nil
				}
			}
			if err := postResponse(client, event, "unrecognized command, msg me `help` for a list of all commands"); err != nil {
				return false, fmt.Errorf("failed all attempts to post the response to the requested action: %s", event.Text)
			}
			return true, nil
		})
}

func postResponse(client *slack.Client, event *slackevents.MessageEvent, response string) error {
	var lastErr error
	ctx := context.TODO()
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		_, responseTimestamp, err := client.PostMessage(event.Channel, slack.MsgOptionText(response, false))
		if err != nil {
			lastErr = err
			return false, nil
		}
		klog.Infof("Posted response to UserID: %s (event: `%s`) at %s", event.User, event.Text, responseTimestamp)
		return true, nil
	})
	if err != nil {
		klog.Errorf("Failed to post response to UserID: %s; (event: `%s`) at %d; %v", event.User, event.Text, (time.Now()).Unix(), err)
		return lastErr
	}
	return nil
}

// GenerateHelpOverviewMessage creates the help overview message content
func GenerateHelpOverviewMessage(allowPrivate bool) string {
	helpMessage := "*🤖 Cluster Bot - Quick Start*\n\n"

	// Common commands
	helpMessage += "*Most Used Commands:*\n"
	helpMessage += "• `help launch` - Launch OpenShift clusters\n"
	helpMessage += "• `help rosa` - ROSA (Red Hat OpenShift Service on AWS)\n"
	helpMessage += "• `list` - See active clusters\n"
	helpMessage += "• `done` - Terminate your cluster\n"
	helpMessage += "• `auth` - Get cluster credentials\n\n"

	// All available commands with detailed usage
	helpMessage += "\n*All Commands:*\n"

	helpMessage += "\n**Cluster Launching:**\n"
	helpMessage += "• `launch <image_or_version_or_prs> <options>` - Launch OpenShift clusters using images, versions, or PRs\n"
	helpMessage += "• `workflow-launch <name> <image_or_version_or_prs> <parameters>` - Launch using custom workflows\n"

	helpMessage += "\n**ROSA Clusters:**\n"
	helpMessage += "• `rosa create <version> <duration>` - Create ROSA clusters with automatic teardown\n"
	helpMessage += "• `rosa lookup <version>` - Find supported ROSA versions by prefix\n"
	helpMessage += "• `rosa describe <cluster>` - Display details of ROSA cluster\n"

	helpMessage += "\n**Cluster Management:**\n"
	helpMessage += "• `list` - See who is using all the clusters\n"
	helpMessage += "• `done` - Terminate your running cluster\n"
	helpMessage += "• `auth` - Get credentials for your most recent cluster\n"
	helpMessage += "• `refresh` - Retry fetching credentials if cluster marked as failed\n"

	helpMessage += "\n**Testing:**\n"
	helpMessage += "• `test <name> <image_or_version_or_prs> <options>` - Run test suites from images or PRs\n"
	helpMessage += "• `test upgrade <from> <to> <options>` - Run upgrade tests between release images\n"
	helpMessage += "• `workflow-test <name> <image_or_version_or_prs> <parameters>` - Test using custom workflows\n"
	helpMessage += "• `workflow-upgrade <name> <from> <to> <parameters>` - Custom upgrade workflows\n"

	helpMessage += "\n**Building:**\n"
	helpMessage += "• `build <pullrequest>` - Create release image from PRs (preserved 12h)\n"
	helpMessage += "• `catalog build <pullrequest> <bundle_name>` - Create operator catalog from PR\n"

	helpMessage += "\n**Information:**\n"
	helpMessage += "• `version` - Report the bot version\n"
	helpMessage += "• `lookup <image_or_version_or_prs> <architecture>` - Get version info\n"

	if allowPrivate {
		helpMessage += "\n**MCE Clusters (Private):**\n"
		helpMessage += "• `mce create <image_or_version_or_prs> <duration> <platform>` - Create clusters using Hive and MCE\n"
		helpMessage += "• `mce auth <name>` - Get kubeconfig and kubeadmin password for MCE cluster\n"
		helpMessage += "• `mce delete <cluster_name>` - Delete MCE cluster\n"
		helpMessage += "• `mce list <all>` - List active MCE clusters\n"
		helpMessage += "• `mce lookup` - List available MCE versions\n"
	}

	helpMessage += "\n*Category Help:*\n"
	helpMessage += "• `help launch` - Cluster launching\n"
	helpMessage += "• `help rosa` - ROSA clusters\n"
	helpMessage += "• `help test` - Testing & workflows\n"
	helpMessage += "• `help build` - Build images\n"
	helpMessage += "• `help manage` - Cluster management\n"

	if allowPrivate {
		helpMessage += "• `help mce` - MCE clusters (private)\n"
	}

	helpMessage += "\n*Examples:*\n"
	helpMessage += "• `launch 4.19 aws` - Launch OpenShift 4.19 on AWS\n"
	helpMessage += "• `rosa create 4.19 3h` - Create ROSA cluster for 3 hours\n"
	helpMessage += "• `help launch` - See all launch options\n\n"

	helpMessage += "*Additional Links*\n"
	helpMessage += "Please check out our <https://github.com/openshift/ci-chat-bot/blob/master/docs/FAQ.md|Frequently Asked Questions> for more information.\n"
	helpMessage += "You can also reach out to us in <https://redhat-internal.slack.com/archives/CNHC2DK2M|#forum-ocp-crt> for more information.\n"

	return helpMessage
}

// GenerateLaunchHelpMessage creates the comprehensive launch help message
func GenerateLaunchHelpMessage() string {
	helpMessage := "*🚀 Cluster Launching*\n\n"

	helpMessage += "*launch*\n"
	helpMessage += "```\nlaunch <image_or_version_or_prs> <options>\n```\n"
	helpMessage += "Launch an OpenShift cluster using a known image, version, or PR(s).\n\n"

	helpMessage += "*Available Platforms:*\n"
	helpMessage += "• `aws`, `gcp`, `azure`, `vsphere`, `metal`, `hypershift-hosted`\n"
	helpMessage += "• `ovirt`, `openstack`, `nutanix`, `alibaba`, `azure-stackhub`\n\n"

	helpMessage += "*Available Architectures:*\n"
	helpMessage += "• `amd64` (default), `arm64`, `multi`\n\n"

	helpMessage += "*Common Parameters:*\n"
	helpMessage += "• `compact` - 3-node cluster\n"
	helpMessage += "• `large`, `xlarge` - Larger node sizes\n"
	helpMessage += "• `proxy` - Enable proxy configuration\n"
	helpMessage += "• `fips` - FIPS-compliant cluster\n"
	helpMessage += "• `ipv6`, `dualstack` - IPv6 networking\n"
	helpMessage += "• `techpreview` - Enable tech preview features\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `launch 4.19 aws` - Latest 4.19 on AWS\n"
	helpMessage += "• `launch 4.19 gcp,arm64` - ARM64 cluster on GCP\n"
	helpMessage += "• `launch 4.19 azure,compact,fips` - Compact FIPS cluster\n"
	helpMessage += "• `launch openshift/installer#123 metal` - Test PR on bare metal\n"

	return helpMessage
}

// GenerateRosaHelpMessage creates the comprehensive ROSA help message
func GenerateRosaHelpMessage() string {
	helpMessage := "*☁️ ROSA (Red Hat OpenShift Service on AWS)*\n\n"

	helpMessage += "*rosa create*\n"
	helpMessage += "```\nrosa create <version> <duration>\n```\n"
	helpMessage += "Create a ROSA cluster on AWS with automatic teardown.\n\n"

	helpMessage += "*rosa describe*\n"
	helpMessage += "```\nrosa describe <cluster_name>\n```\n"
	helpMessage += "Get detailed information about a ROSA cluster.\n\n"

	helpMessage += "*Common Options:*\n"
	helpMessage += "• Duration: `1h`, `3h`, `24h`, `48h`\n"
	helpMessage += "• Versions: Latest stable releases\n"
	helpMessage += "• Automatic cleanup after expiration\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `rosa create 4.19 3h` - Create 4.19 cluster for 3 hours\n"
	helpMessage += "• `rosa create 4.18 24h` - Create 4.18 cluster for 24 hours\n"
	helpMessage += "• `rosa describe my-cluster` - Get cluster details\n"

	return helpMessage
}

// GenerateTestHelpMessage creates the comprehensive testing help message
func GenerateTestHelpMessage() string {
	helpMessage := "*🧪 Testing & Workflows*\n\n"

	helpMessage += "*test*\n"
	helpMessage += "```\ntest <image_or_version> <platform> <test_suite>\n```\n"
	helpMessage += "Run test suites against OpenShift clusters.\n\n"

	helpMessage += "*test upgrade*\n"
	helpMessage += "```\ntest upgrade <from_version> <to_version> <platform>\n```\n"
	helpMessage += "Test cluster upgrade scenarios.\n\n"

	helpMessage += "*workflow-test*\n"
	helpMessage += "```\nworkflow-test <workflow_name> <parameters>\n```\n"
	helpMessage += "Execute specific CI workflows for testing.\n\n"

	helpMessage += "*Test Types:*\n"
	helpMessage += "• Conformance tests\n"
	helpMessage += "• Upgrade scenarios\n"
	helpMessage += "• Custom workflow validation\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `test 4.19 aws conformance` - Run conformance tests\n"
	helpMessage += "• `test upgrade 4.18 4.19 gcp` - Test upgrade path\n"
	helpMessage += "• `workflow-test installer-validation` - Run installer tests\n"

	return helpMessage
}

// GenerateBuildHelpMessage creates the comprehensive build help message
func GenerateBuildHelpMessage() string {
	helpMessage := "*🔨 Building Images*\n\n"

	helpMessage += "*build*\n"
	helpMessage += "```\nbuild <repository> <pr_number> <target>\n```\n"
	helpMessage += "Build custom images from pull requests.\n\n"

	helpMessage += "*catalog build*\n"
	helpMessage += "```\ncatalog build <operator> <version>\n```\n"
	helpMessage += "Build operator catalog images.\n\n"

	helpMessage += "*Build Targets:*\n"
	helpMessage += "• `installer` - Build installer images\n"
	helpMessage += "• `release` - Build release payload\n"
	helpMessage += "• `operator` - Build operator images\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `build openshift/installer#123 installer` - Build installer from PR\n"
	helpMessage += "• `catalog build my-operator v1.0` - Build operator catalog\n"
	helpMessage += "• `build machine-config-operator#456 release` - Build MCO changes\n"

	return helpMessage
}

// GenerateManageHelpMessage creates the comprehensive management help message
func GenerateManageHelpMessage() string {
	helpMessage := "*⚙️ Cluster Management*\n\n"

	helpMessage += "*list*\n"
	helpMessage += "```\nlist [user]\n```\n"
	helpMessage += "Show active clusters (all or for specific user).\n\n"

	helpMessage += "*done*\n"
	helpMessage += "```\ndone [cluster_name]\n```\n"
	helpMessage += "Terminate and cleanup clusters.\n\n"

	helpMessage += "*auth*\n"
	helpMessage += "```\nauth <cluster_name>\n```\n"
	helpMessage += "Get cluster credentials and connection info.\n\n"

	helpMessage += "*refresh*\n"
	helpMessage += "```\nrefresh <cluster_name>\n```\n"
	helpMessage += "Refresh cluster status and extend lifetime.\n\n"

	helpMessage += "*lookup*\n"
	helpMessage += "```\nlookup <job_id>\n```\n"
	helpMessage += "Find cluster information by job ID.\n\n"

	helpMessage += "*version*\n"
	helpMessage += "```\nversion\n```\n"
	helpMessage += "Show cluster-bot version information.\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `list` - Show all your clusters\n"
	helpMessage += "• `done my-cluster` - Terminate specific cluster\n"
	helpMessage += "• `auth test-cluster` - Get kubeconfig credentials\n"
	helpMessage += "• `refresh cluster-123` - Extend cluster lifetime\n"

	return helpMessage
}

// GenerateMceHelpMessage creates the comprehensive MCE help message
func GenerateMceHelpMessage() string {
	helpMessage := "*🏢 MCE (Multi-Cluster Engine) - Private Commands*\n\n"

	helpMessage += "*mce create*\n"
	helpMessage += "```\nmce create <hub_version> <managed_count> <platform>\n```\n"
	helpMessage += "Create MCE hub with managed clusters.\n\n"

	helpMessage += "*mce describe*\n"
	helpMessage += "```\nmce describe <hub_name>\n```\n"
	helpMessage += "Get detailed MCE hub information.\n\n"

	helpMessage += "*MCE Features:*\n"
	helpMessage += "• Multi-cluster management\n"
	helpMessage += "• Cluster lifecycle automation\n"
	helpMessage += "• Policy and governance\n"
	helpMessage += "• Application deployment\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `mce create 2.6 3 aws` - Create hub with 3 managed clusters\n"
	helpMessage += "• `mce describe my-hub` - Get hub cluster details\n"

	helpMessage += "\n*Note: MCE commands require special authorization.*\n"

	return helpMessage
}

// HelpOverview displays a categorized overview of available commands instead of overwhelming users with all commands at once
func HelpOverview(client *slack.Client, event *slackevents.MessageEvent, botCommands []parser.BotCommand, allowPrivate bool) {
	helpMessage := GenerateHelpOverviewMessage(allowPrivate)
	if err := postResponse(client, event, helpMessage); err != nil {
		klog.Errorf("failed to post help overview: %v", err)
	}
}

// HelpSpecific shows detailed help for a specific command category (e.g., "launch", "rosa") with usage examples
func HelpSpecific(client *slack.Client, event *slackevents.MessageEvent, category string, botCommands []parser.BotCommand, allowPrivate bool) {
	category = strings.ToLower(category)

	// Use dedicated help functions for comprehensive help
	switch category {
	case "launch", "cluster":
		helpMessage := GenerateLaunchHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post launch help: %v", err)
		}
		return
	case "rosa":
		helpMessage := GenerateRosaHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post rosa help: %v", err)
		}
		return
	case "test", "testing":
		helpMessage := GenerateTestHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post test help: %v", err)
		}
		return
	case "build", "building":
		helpMessage := GenerateBuildHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post build help: %v", err)
		}
		return
	case "manage", "management":
		helpMessage := GenerateManageHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post manage help: %v", err)
		}
		return
	case "mce":
		if allowPrivate {
			helpMessage := GenerateMceHelpMessage()
			if err := postResponse(client, event, helpMessage); err != nil {
				klog.Errorf("failed to post mce help: %v", err)
			}
			return
		} else {
			if err := postResponse(client, event, "MCE commands are not available to you. Please contact an administrator."); err != nil {
				klog.Errorf("failed to post mce access error: %v", err)
			}
			return
		}
	}

	// Try to find a specific command for unknown categories
	var relevantCommands []parser.BotCommand
	var categoryTitle string

	// Try to find a specific command
	for _, cmd := range botCommands {
		if cmd.IsPrivate() && !allowPrivate {
			continue
		}
		tokens := cmd.Tokenize()
		if len(tokens) > 0 && strings.ToLower(tokens[0].Word) == category {
			relevantCommands = append(relevantCommands, cmd)
			categoryTitle = fmt.Sprintf("Command: %s", tokens[0].Word)
			break
		}
	}

	if len(relevantCommands) == 0 {
		suggestion := findCommandSuggestion(category, botCommands, allowPrivate)
		helpMessage := fmt.Sprintf("❓ Unknown help topic: '%s'\n", category)
		if suggestion != "" {
			helpMessage += fmt.Sprintf("Did you mean: `help %s`?\n\n", suggestion)
		}
		helpMessage += "Available help topics:\n"
		helpMessage += "• `help launch` - Cluster launching\n"
		helpMessage += "• `help rosa` - ROSA clusters\n"
		helpMessage += "• `help test` - Testing commands\n"
		helpMessage += "• `help build` - Build commands\n"
		helpMessage += "• `help manage` - Management commands\n"
		if allowPrivate {
			helpMessage += "• `help mce` - MCE commands\n"
		}
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post unknown help topic message: %v", err)
		}
		return
	}

	helpMessage := fmt.Sprintf("*%s*\n\n", categoryTitle)

	for _, command := range relevantCommands {
		if command.IsPrivate() && !allowPrivate {
			continue
		}

		tokens := command.Tokenize()

		// Command name
		helpMessage += "*"
		for _, token := range tokens {
			if !token.IsParameter() {
				helpMessage += token.Word + " "
			}
		}
		helpMessage += "*\n"

		// Usage
		helpMessage += "```\n"
		for _, token := range tokens {
			helpMessage += token.Word + " "
		}
		helpMessage += "```\n"

		// Description
		if len(command.Definition().Description) > 0 {
			helpMessage += command.Definition().Description + "\n"
		}

		// Example
		if len(command.Definition().Example) > 0 {
			helpMessage += "Example: `" + command.Definition().Example + "`\n"
		}

		helpMessage += "\n"
	}

	if len(helpMessage) > 3000 { // Slack message limit
		helpMessage = helpMessage[:2900] + "...\n\n_Message truncated - try a more specific help topic_"
	}

	if err := postResponse(client, event, helpMessage); err != nil {
		klog.Errorf("failed to post specific help: %v", err)
	}
}

func findCommandSuggestion(input string, botCommands []parser.BotCommand, allowPrivate bool) string {
	input = strings.ToLower(input)
	categories := []string{"launch", "rosa", "test", "build", "manage"}
	if allowPrivate {
		categories = append(categories, "mce")
	}

	// Check categories first
	for _, cat := range categories {
		if strings.Contains(cat, input) || strings.Contains(input, cat) {
			return cat
		}
	}

	// Check individual commands
	for _, cmd := range botCommands {
		if cmd.IsPrivate() && !allowPrivate {
			continue
		}
		tokens := cmd.Tokenize()
		if len(tokens) > 0 {
			cmdName := strings.ToLower(tokens[0].Word)
			if strings.Contains(cmdName, input) || strings.Contains(input, cmdName) {
				return tokens[0].Word
			}
		}
	}

	return ""
}
