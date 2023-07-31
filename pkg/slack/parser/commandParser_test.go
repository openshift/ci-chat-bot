package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/openshift/ci-chat-bot/pkg/manager"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// this is a partial copy of pkg/slack/slack.go
var supportedCommands = []BotCommand{
	NewBotCommand("launch <image_or_version_or_prs> <options>", &CommandDefinition{
		Description: fmt.Sprintf("Launch an OpenShift cluster using a known image, version, or PR(s). You may omit both arguments. Arguments can be specified as any number of comma-delimited values. Use `nightly` for the latest OCP build, `ci` for the the latest CI build, provide a version directly from any listed on https://amd64.ocp.releases.ci.openshift.org, a stream name (4.14.0-0.ci, 4.14.0-0.nightly, etc), a major/minor `X.Y` to load the \"next stable\" version, from nightly, for that version (`4.14`), `<org>/<repo>#<pr>` to launch from any combination of PRs, or an image for the first argument. Options is a comma-delimited list of variations including platform (%s), architecture (%s), and variant (%s).",
			strings.Join(codeSlice(manager.SupportedPlatforms), ", "),
			strings.Join(codeSlice(manager.SupportedArchitectures), ", "),
			strings.Join(codeSlice(manager.SupportedParameters), ", ")),
		Example: "launch 4.14,openshift/installer#7160,openshift/machine-config-operator#3688 gcp,techpreview",
		Handler: emptyHandler,
	}),
	NewBotCommand("rosa create <version> <duration>", &CommandDefinition{
		Description: "Launch an cluster in ROSA. Only GA Openshift versions are supported at the moment.",
		Example:     "rosa create 4.12 3h",
		Handler:     emptyHandler,
	}),
	NewBotCommand("rosa lookup <version>", &CommandDefinition{
		Description: "Find openshift version(s) with provided prefix that is supported in ROSA.",
		Example:     "rosa lookup 4.12",
		Handler:     emptyHandler,
	}),
	NewBotCommand("list", &CommandDefinition{
		Description: "See who is hogging all the clusters.",
		Handler:     emptyHandler,
	}),
	NewBotCommand("test upgrade <from> <to> <options>", &CommandDefinition{
		Description: fmt.Sprintf("Run the upgrade tests between two release images. The arguments may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. You may change the upgrade test by passing `test=NAME` in options with one of %s", strings.Join(codeSlice(manager.SupportedUpgradeTests), ", ")),
		Example:     "test upgrade 4.12 4.14 aws",
		Handler:     emptyHandler,
	}),
	NewBotCommand("test <name> <image_or_version_or_prs> <options>", &CommandDefinition{
		Description: fmt.Sprintf("Run the requested test suite from an image or release or built PRs. Supported test suites are %s. The from argument may be a pull spec of a release image or tags from https://amd64.ocp.releases.ci.openshift.org. ", strings.Join(codeSlice(manager.SupportedTests), ", ")),
		Example:     "test e2e 4.14 vsphere",
		Handler:     emptyHandler,
	}),
	NewBotCommand("build <pullrequest>", &CommandDefinition{
		Description: "Create a new release image from one or more pull requests. The successful build location will be sent to you when it completes and then preserved for 12 hours.  To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
		Example:     "build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396",
		Handler:     emptyHandler,
	}),
	NewBotCommand("lookup <image_or_version_or_prs> <architecture>", &CommandDefinition{
		Description: "Get info about a version.",
		Example:     "lookup 4.14 arm64",
		Handler:     emptyHandler,
	}),
	NewBotCommand("catalog build <pullrequest> <bundle_name>", &CommandDefinition{
		Description: "Create an operator, bundle, and catalof from a pull request. The successful build location will be sent to you when it completes and then preserved for 12 hours.  To obtain a pull secret use `oc registry login --to /path/to/pull-secret` after using `oc login` to login to the relevant CI cluster.",
		Example:     "catalog build openshift/aws-efs-csi-driver-operator#75 aws-efs-csi-driver-operator-bundle",
		Handler:     emptyHandler,
	}),
}

func codeSlice(items []string) []string {
	code := make([]string, 0, len(items))
	for _, item := range items {
		code = append(code, fmt.Sprintf("`%s`", item))
	}
	return code
}
func emptyHandler(client *slack.Client, jobManager manager.JobManager, event *slackevents.MessageEvent, properties *Properties) string {
	return ""
}

func TestMatch(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		command       string
		match         int  // index of item in supportedCommands that the command should match; -1 for no match
		allowMultiple bool // we need this because test <name> ... matches test upgrade <from> ...
		properties    *Properties
	}{{
		command: "launch",
		match:   0,
		properties: &Properties{
			PropertyMap: map[string]string{},
		},
	}, {
		command: "launch 4.14,openshift/installer#7160,openshift/machine-config-operator#3688 gcp,techpreview",
		match:   0,
		properties: &Properties{
			PropertyMap: map[string]string{
				"image_or_version_or_prs": "4.14,openshift/installer#7160,openshift/machine-config-operator#3688",
				"options":                 "gcp,techpreview",
			},
		},
	}, {
		command: "rosa create",
		match:   1,
		properties: &Properties{
			PropertyMap: map[string]string{},
		},
	}, {
		command: "rosa create 4.12",
		match:   1,
		properties: &Properties{
			PropertyMap: map[string]string{
				"version": "4.12",
			},
		},
	}, {
		command: "rosa create 4.12 3h",
		match:   1,
		properties: &Properties{
			PropertyMap: map[string]string{
				"version":  "4.12",
				"duration": "3h",
			},
		},
	}, {
		command: "rosa lookup 4.12",
		match:   2,
		properties: &Properties{
			PropertyMap: map[string]string{
				"version": "4.12",
			},
		},
	}, {
		command:    "rosa launch 4.12",
		match:      -1,
		properties: nil,
	}, {
		command: "list",
		match:   3,
		properties: &Properties{
			PropertyMap: map[string]string{},
		},
	}, {
		command:       "test upgrade 4.12 4.14 aws",
		match:         4,
		allowMultiple: true,
		properties: &Properties{
			PropertyMap: map[string]string{
				"from":    "4.12",
				"to":      "4.14",
				"options": "aws",
			},
		},
	}, {
		command: "test e2e 4.14 vsphere",
		match:   5,
		properties: &Properties{
			PropertyMap: map[string]string{
				"name":                    "e2e",
				"image_or_version_or_prs": "4.14",
				"options":                 "vsphere",
			},
		},
	}, {
		command: "build openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396",
		match:   6,
		properties: &Properties{
			PropertyMap: map[string]string{
				"pullrequest": "openshift/operator-framework-olm#68,operator-framework/operator-marketplace#396",
			},
		},
	}, {
		command: "lookup 4.14 arm64",
		match:   7,
		properties: &Properties{
			PropertyMap: map[string]string{
				"image_or_version_or_prs": "4.14",
				"architecture":            "arm64",
			},
		},
	}, {
		command: "catalog build openshift/aws-efs-csi-driver-operator#75 aws-efs-csi-driver-operator-bundle",
		match:   8,
		properties: &Properties{
			PropertyMap: map[string]string{
				"pullrequest": "openshift/aws-efs-csi-driver-operator#75",
				"bundle_name": "aws-efs-csi-driver-operator-bundle",
			},
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.command, func(t *testing.T) {
			var properties *Properties
			matchIndex := -1
			for index, parser := range supportedCommands {
				if props, isMatch := parser.Match(tc.command); isMatch {
					if matchIndex != -1 {
						t.Fatal("Multiple matches found")
					}
					matchIndex = index
					properties = props
					if tc.allowMultiple {
						break
					}
				}
			}
			if matchIndex != tc.match {
				t.Fatalf("Incorrectly matched to %d instead of %d", matchIndex, tc.match)
			}
			// don't check properties if there is no match
			if tc.match == -1 {
				return
			}
			if len(properties.PropertyMap) != len(tc.properties.PropertyMap) {
				t.Fatalf("Actual properties (%+v) do not match expected properties (%+v)", properties.PropertyMap, tc.properties.PropertyMap)
			}
			for key, value := range properties.PropertyMap {
				if value != tc.properties.PropertyMap[key] {
					t.Errorf("Actual property (`%s` == `%s`) does not match expected property (`%s` == `%s`)", key, value, key, tc.properties.PropertyMap[key])
				}
			}
		})
	}
}
