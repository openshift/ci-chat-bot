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

const (
	// Slack message size limits
	SlackMessageLimit         = 3000
	SlackMessageTruncateLimit = 2900

	// Help categories
	HelpCategoryLaunch = "launch"
	HelpCategoryRosa   = "rosa"
	HelpCategoryTest   = "test"
	HelpCategoryBuild  = "build"
	HelpCategoryManage = "manage"
	HelpCategoryMce    = "mce"
)

// HelpCategories defines the standard help categories (excluding MCE which is private)
var HelpCategories = []string{
	HelpCategoryLaunch,
	HelpCategoryRosa,
	HelpCategoryTest,
	HelpCategoryBuild,
	HelpCategoryManage,
}

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

	helpMessage += "\n*Cluster Launching:*\n"
	helpMessage += "• `launch <image_or_version_or_prs> <options>` - Launch OpenShift clusters using images, versions, or PRs\n"
	helpMessage += "• `workflow-launch <name> <image_or_version_or_prs> <parameters>` - Launch using custom workflows\n"

	helpMessage += "\n*ROSA Clusters:*\n"
	helpMessage += "• `rosa create <version> <duration>` - Create ROSA clusters with automatic teardown\n"
	helpMessage += "• `rosa lookup <version>` - Find supported ROSA versions by prefix\n"
	helpMessage += "• `rosa describe <cluster>` - Display details of ROSA cluster\n"

	helpMessage += "\n*Cluster Management:*\n"
	helpMessage += "• `list` - See who is using all the clusters\n"
	helpMessage += "• `done` - Terminate your running cluster\n"
	helpMessage += "• `auth` - Get credentials for your most recent cluster\n"
	helpMessage += "• `refresh` - Retry fetching credentials if cluster marked as failed\n"
	helpMessage += "• `request <resource> \"<justification>\"` - Request access to GCP workspace (7-day access)\n"
	helpMessage += "• `revoke <resource>` - Remove your GCP workspace access early\n"

	helpMessage += "\n*Testing:*\n"
	helpMessage += "• `test <name> <image_or_version_or_prs> <options>` - Run test suites from images or PRs\n"
	helpMessage += "• `test upgrade <from> <to> <options>` - Run upgrade tests between release images\n"
	helpMessage += "• `workflow-test <name> <image_or_version_or_prs> <parameters>` - Test using custom workflows\n"
	helpMessage += "• `workflow-upgrade <name> <from> <to> <parameters>` - Custom upgrade workflows\n"

	helpMessage += "\n*Building:*\n"
	helpMessage += "• `build <version>,<pullrequest>` - Create release image from PRs (preserved 1 week)\n"
	helpMessage += "• `catalog build <version>,<pullrequest> <bundle_name>` - Create operator catalog from PR\n"

	helpMessage += "\n*Information:*\n"
	helpMessage += "• `version` - Report the bot version\n"
	helpMessage += "• `lookup <image_or_version_or_prs> <architecture>` - Get version info\n"

	if allowPrivate {
		helpMessage += "\n*MCE Clusters (Private):*\n"
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
	helpMessage += "• `launch 4.19 hypershift-hosted` - Launch OpenShift 4.19 on Hypershift\n"
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

	helpMessage += "*Input Formats for <image_or_version_or_prs>:*\n"
	helpMessage += "• `nightly` - Latest OCP nightly build\n"
	helpMessage += "• `ci` - Latest CI build\n"
	helpMessage += "• `4.19` - Major.minor for next stable from nightly\n"
	helpMessage += "• `4.19.0-0.nightly` - Specific stream name\n"
	helpMessage += "• `openshift/installer#123` - Pull request(s)\n"
	helpMessage += "• Direct image pull spec from releases page\n\n"

	helpMessage += "*Quick Reference - Common Configurations:*\n"
	helpMessage += "• Basic AWS: `launch 4.19 aws`\n"
	helpMessage += "• Compact cluster: `launch 4.19 aws,compact`\n"
	helpMessage += "• ARM on GCP: `launch 4.19 gcp,arm64`\n"
	helpMessage += "• Secure cluster: `launch 4.19 aws,fips,private`\n"
	helpMessage += "• Test environment: `launch 4.19 metal,compact,techpreview`\n\n"

	helpMessage += "*Important Notes:*\n"
	helpMessage += "• Must contain an OpenShift version\n"
	helpMessage += "• Options can be omitted (defaults: `hypershift-hosted`,`amd64`)\n"
	helpMessage += "• Options is a comma-delimited list including platform, architecture, and variants\n"
	helpMessage += "• Order doesn't matter except for readability\n\n"

	helpMessage += "*Platform (choose one):*\n"
	helpMessage += "• Most common: `aws`, `gcp`, `azure`, `vsphere`, `metal`\n"
	helpMessage += "• Cloud: `alibaba`, `nutanix`, `openstack`\n"
	helpMessage += "• Specialized: `ovirt`, `hypershift-hosted`, `hypershift-hosted-powervs`, `azure-stackhub`\n\n"

	helpMessage += "*Architecture (choose one, optional):*\n"
	helpMessage += "• `amd64` (default), `arm64`, `multi`\n\n"

	helpMessage += "*Networking (choose one primary, optional):*\n"
	helpMessage += "• Primary: `ovn` (default), `sdn`, `kuryr`\n"
	helpMessage += "• Modifiers: `ovn-hybrid`, `proxy`\n"
	helpMessage += "• IP versions: `ipv4` (default), `ipv6`, `dualstack`, `dualstack-primaryv6`\n\n"

	helpMessage += "*Cluster Size & Configuration (combine as needed):*\n"
	helpMessage += "• Size: `compact` (3-node), `single-node`, `large`, `xlarge`\n"
	helpMessage += "• Zones: `multi-zone`, `multi-zone-techpreview`\n\n"

	helpMessage += "*Security & Compliance (combine as needed):*\n"
	helpMessage += "• `fips`, `private`, `rt`, `no-capabilities`\n\n"

	helpMessage += "*Advanced Options (combine as needed):*\n"
	helpMessage += "• Installation: `upi`, `preserve-bootstrap`\n"
	helpMessage += "• Runtime: `techpreview`\n"
	helpMessage += "• Infrastructure: `mirror`, `shared-vpc`, `no-spot`, `virtualization-support`\n"
	helpMessage += "• Special: `test`, `bundle`, `nfv`\n\n"

	helpMessage += "*Option Guidelines:*\n"
	helpMessage += "• Start with platform (aws, gcp, etc.)\n"
	helpMessage += "• Add architecture if not default (arm64, multi)\n"
	helpMessage += "• Add networking if needed (sdn, kuryr, ipv6)\n"
	helpMessage += "• Add size/features last (compact, fips, techpreview)\n"
	helpMessage += "• Bot will validate combinations and inform about conflicts\n\n"

	helpMessage += "*Examples (Simple to Complex):*\n"
	helpMessage += "• `launch 4.19` - Default: latest 4.19 on `hypershift-hosted` `amd64`\n"
	helpMessage += "• `launch nightly aws` - Latest nightly build\n"
	helpMessage += "• `launch 4.19 gcp,arm64` - Different platform + architecture\n"
	helpMessage += "• `launch ci azure,compact,ovn` - CI build + size + networking\n"
	helpMessage += "• `launch 4.19 aws,arm64,fips,private` - Security-focused cluster\n"
	helpMessage += "• `launch 4.19.0-0.nightly metal,single-node,techpreview` - Advanced config\n"
	helpMessage += "• `launch 4.19,openshift/installer#123 vsphere,multi,techpreview` - PR testing\n"
	helpMessage += "• `launch 4.19,openshift/installer#123,openshift/mco#456 aws,multi-zone` - Multi-PR\n"

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
	helpMessage += "```\ntest <name> <image_or_version_or_prs> <options>\n```\n"
	helpMessage += "Run the requested test suite from an image, release, or built PRs.\n\n"

	helpMessage += "*Available Test Suites:*\n"
	helpMessage += "• `e2e` - End-to-end conformance tests\n"
	helpMessage += "• `e2e-serial` - Serial end-to-end tests\n"
	helpMessage += "• `e2e-all` - All end-to-end tests\n"
	helpMessage += "• `e2e-disruptive` - Disruptive tests\n"
	helpMessage += "• `e2e-disruptive-all` - All disruptive tests\n"
	helpMessage += "• `e2e-builds` - Build-related tests\n"
	helpMessage += "• `e2e-image-ecosystem` - Image ecosystem tests\n"
	helpMessage += "• `e2e-image-registry` - Image registry tests\n"
	helpMessage += "• `e2e-network-stress` - Network stress tests\n\n"

	helpMessage += "*test upgrade*\n"
	helpMessage += "```\ntest upgrade <from> <to> <options>\n```\n"
	helpMessage += "Run upgrade tests between two release images.\n\n"

	helpMessage += "*Upgrade Test Options:*\n"
	helpMessage += "• `e2e-upgrade` - Standard upgrade test (default)\n"
	helpMessage += "• `e2e-upgrade-all` - All upgrade tests\n"
	helpMessage += "• `e2e-upgrade-partial` - Partial upgrade test\n"
	helpMessage += "• `e2e-upgrade-rollback` - Upgrade rollback test\n"
	helpMessage += "Pass as `test=NAME` in options (e.g., `test=e2e-upgrade-all`)\n\n"

	helpMessage += "*workflow-test*\n"
	helpMessage += "```\nworkflow-test <name> <image_or_version_or_prs> <parameters>\n```\n"
	helpMessage += "Start test using the requested workflow.\n\n"

	helpMessage += "*workflow-upgrade*\n"
	helpMessage += "```\nworkflow-upgrade <name> <from> <to> <parameters>\n```\n"
	helpMessage += "Run custom upgrade using the requested workflow.\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `test e2e 4.19 aws` - Run e2e tests on AWS\n"
	helpMessage += "• `test e2e-serial 4.19 gcp` - Run serial tests\n"
	helpMessage += "• `test upgrade 4.17 4.19 aws` - Test upgrade path\n"
	helpMessage += "• `test upgrade 4.17 4.19 aws,test=e2e-upgrade-all` - All upgrade tests\n"
	helpMessage += "• `workflow-test openshift-e2e-gcp 4.19` - Run GCP workflow\n"
	helpMessage += "• `workflow-upgrade openshift-upgrade-azure-ovn 4.17 4.19 azure` - Custom upgrade\n"

	return helpMessage
}

// GenerateBuildHelpMessage creates the comprehensive build help message
func GenerateBuildHelpMessage() string {
	helpMessage := "*🔨 Building Images*\n\n"

	helpMessage += "*build*\n"
	helpMessage += "```\nbuild <version>,<organization>/<repository>#<pr_number>\n```\n"
	helpMessage += "Build custom images from pull requests.\n\n"

	helpMessage += "*catalog build*\n"
	helpMessage += "```\ncatalog build <version>,<organization>/<repository>#<pr_number> <bundle_name>\n```\n"
	helpMessage += "Build operator catalog images.\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `build 4.19,openshift/installer#123` - Build installer images from PR\n"
	helpMessage += "• `build 4.19,openshift/machine-config-operator#456` - Build MCO change\n"
	helpMessage += "• `build 4.19,openshift/origin#49563,openshift/kubernetes#731,openshift/machine-api-operator#831` - Build with multiple PRs\n"
	helpMessage += "• `catalog build 4.19,openshift/aws-efs-csi-driver-operator#84 aws-efs-csi-driver-operator-bundle` - Build operator catalog\n"

	return helpMessage
}

// GenerateManageHelpMessage creates the comprehensive management help message
func GenerateManageHelpMessage() string {
	helpMessage := "*⚙️ Cluster Management*\n\n"

	helpMessage += "*list*\n"
	helpMessage += "```\nlist\n```\n"
	helpMessage += "Show active clusters (all or for specific user).\n\n"

	helpMessage += "*done*\n"
	helpMessage += "```\ndone\n```\n"
	helpMessage += "Terminate and cleanup cluster.\n\n"

	helpMessage += "*auth*\n"
	helpMessage += "```\nauth\n```\n"
	helpMessage += "Get cluster credentials and connection info.\n\n"

	helpMessage += "*refresh*\n"
	helpMessage += "```\nrefresh\n```\n"
	helpMessage += "Retry fetching cluster credentials in case of an error.\n\n"

	helpMessage += "*request*\n"
	helpMessage += "```\nrequest <resource> \"<justification>\"\n```\n"
	helpMessage += "Request access to GCP workspace. Access is granted for 7 days and automatically expires. Users cannot extend their access; they must either wait for expiration or use the 'revoke' command to remove access early before requesting new access. Must be a member of Hybrid Platforms organization.\n\n"

	helpMessage += "*revoke*\n"
	helpMessage += "```\nrevoke <resource>\n```\n"
	helpMessage += "Remove your GCP workspace access before it expires. This allows you to immediately revoke access if you no longer need it, rather than waiting for the automatic 7-day expiration.\n\n"

	helpMessage += "*lookup*\n"
	helpMessage += "```\nlookup <version specifier>\n```\n"
	helpMessage += "Find version corresponding to version specifier.\n\n"

	helpMessage += "*version*\n"
	helpMessage += "```\nversion\n```\n"
	helpMessage += "Show cluster-bot version information.\n\n"

	helpMessage += "*Examples:*\n"
	helpMessage += "• `list` - Show all your clusters\n"
	helpMessage += "• `done` - Terminate specific cluster\n"
	helpMessage += "• `auth` - Get kubeconfig credentials\n"
	helpMessage += "• `refresh` - Re-fetch cluster credentials\n"
	helpMessage += "• `request gcp-access \"Need to debug CI infrastructure issues\"` - Request GCP workspace access\n"
	helpMessage += "• `revoke gcp-access` - Remove your GCP workspace access\n"
	helpMessage += "• `lookup nightly` - Find version corresponding to the specified value\n"

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
	case HelpCategoryLaunch, "cluster":
		helpMessage := GenerateLaunchHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post launch help: %v", err)
		}
		return
	case HelpCategoryRosa:
		helpMessage := GenerateRosaHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post rosa help: %v", err)
		}
		return
	case HelpCategoryTest, "testing":
		helpMessage := GenerateTestHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post test help: %v", err)
		}
		return
	case HelpCategoryBuild, "building":
		helpMessage := GenerateBuildHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post build help: %v", err)
		}
		return
	case HelpCategoryManage, "management":
		helpMessage := GenerateManageHelpMessage()
		if err := postResponse(client, event, helpMessage); err != nil {
			klog.Errorf("failed to post manage help: %v", err)
		}
		return
	case HelpCategoryMce:
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

	if len(helpMessage) > SlackMessageLimit {
		helpMessage = helpMessage[:SlackMessageTruncateLimit] + "...\n\n_Message truncated - try a more specific help topic_"
	}

	if err := postResponse(client, event, helpMessage); err != nil {
		klog.Errorf("failed to post specific help: %v", err)
	}
}

func findCommandSuggestion(input string, botCommands []parser.BotCommand, allowPrivate bool) string {
	input = strings.ToLower(input)
	categories := make([]string, len(HelpCategories))
	copy(categories, HelpCategories)
	if allowPrivate {
		categories = append(categories, HelpCategoryMce)
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
