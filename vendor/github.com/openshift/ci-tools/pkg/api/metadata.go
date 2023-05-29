package api

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// IsComplete returns an error if at least one of Org, Repo, Branch members is
// empty, otherwise it returns nil
func (m *Metadata) IsComplete() error {
	var missing []string
	for item, value := range map[string]string{
		"organization": m.Org,
		"repository":   m.Repo,
		"branch":       m.Branch,
	} {
		if value == "" {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		s := ""
		if len(missing) > 1 {
			s = "s"
		}
		return fmt.Errorf("missing item%s: %s", s, strings.Join(missing, ", "))
	}

	return nil
}

// AsString returns a string representation of the metadata suitable for using
// in human-oriented output
func (m *Metadata) AsString() string {
	identifier := fmt.Sprintf("%s/%s@%s", m.Org, m.Repo, m.Branch)
	if m.Variant != "" {
		identifier = fmt.Sprintf("%s [%s]", identifier, m.Variant)
	}
	return identifier
}

// TestNameFromJobName returns the name of the test from a given job name and prefix
func (m *Metadata) TestNameFromJobName(jobName, prefix string) string {
	return strings.TrimPrefix(jobName, m.JobName(prefix, ""))
}

// TestName returns a short name of a test defined in this file, including
// variant, if present
func (m *Metadata) TestName(testName string) string {
	if m.Variant == "" {
		return testName
	}
	return fmt.Sprintf("%s-%s", m.Variant, testName)
}

// JobName returns a full name of a job corresponding to a test defined in this
// file, including variant, if present
func (m *Metadata) JobName(prefix, name string) string {
	return fmt.Sprintf("%s-ci-%s-%s-%s-%s", prefix, m.Org, m.Repo, m.Branch, m.TestName(name))
}

// SimpleJobName returns the job name without the "ci" infix for a  job corresponding to a test defined in this
// file, including variant, if present
func (m *Metadata) SimpleJobName(prefix, name string) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s", prefix, m.Org, m.Repo, m.Branch, m.TestName(name))
}

// Basename returns the unique name for this file in the config
func (m *Metadata) Basename() string {
	basename := strings.Join([]string{m.Org, m.Repo, m.Branch}, "-")
	if m.Variant != "" {
		basename = fmt.Sprintf("%s__%s", basename, m.Variant)
	}
	return fmt.Sprintf("%s.yaml", basename)
}

// JobFilePath returns the file path for the jobs of the supplied suffix type
func (m *Metadata) JobFilePath(suffix string) string {
	return filepath.Join(m.Org, m.Repo, fmt.Sprintf("%s-%s-%s-%s.yaml", m.Org, m.Repo, m.Branch, suffix))
}

// RelativePath returns the path to the config under the root config dir
func (m *Metadata) RelativePath() string {
	return path.Join(m.Org, m.Repo, m.Basename())
}

// ConfigMapName returns the configmap in which we expect this file to be uploaded
func (m *Metadata) ConfigMapName() string {
	return fmt.Sprintf("ci-operator-%s-configs", FlavorForBranch(m.Branch))
}

var ciOPConfigRegex = regexp.MustCompile(`^ci-operator-.+-configs$`)

// IsCiopConfigCM returns true if a given name is a valid ci-operator config ConfigMap
func IsCiopConfigCM(name string) bool {
	return ciOPConfigRegex.MatchString(name)
}

var releaseBranches = regexp.MustCompile(`^(release|enterprise|openshift)-([1-3])\.[0-9]+(?:\.[0-9]+)?$`)
var fourXBranches = regexp.MustCompile(`^(release|enterprise|openshift)-(4\.[0-9]+)$`)

func FlavorForBranch(branch string) string {
	var flavor string
	if branch == "master" || branch == "main" {
		flavor = branch
	} else if m := fourXBranches.FindStringSubmatch(branch); m != nil {
		flavor = m[2] // the 4.x release string
	} else if m := releaseBranches.FindStringSubmatch(branch); m != nil {
		flavor = m[2] + ".x"
	} else {
		flavor = "misc"
	}
	return flavor
}

func LogFieldsFor(metadata Metadata) logrus.Fields {
	return logrus.Fields{
		"org":     metadata.Org,
		"repo":    metadata.Repo,
		"branch":  metadata.Branch,
		"variant": metadata.Variant,
	}
}

func BuildCacheFor(metadata Metadata) ImageStreamTagReference {
	tag := metadata.Branch
	if metadata.Variant != "" {
		tag = fmt.Sprintf("%s-%s", tag, metadata.Variant)
	}
	return ImageStreamTagReference{
		Namespace: "build-cache",
		Name:      fmt.Sprintf("%s-%s", metadata.Org, metadata.Repo),
		Tag:       tag,
	}
}

func ImageVersionLabel(fromTag PipelineImageStreamTagReference) string {
	return fmt.Sprintf("io.openshift.ci.from.%s", fromTag)
}

var testPathRegex = regexp.MustCompile(`(?P<org>[^/]+)/(?P<repo>[^@]+)@(?P<branch>[^:]+):(?P<test>.+)`)

func MetadataTestFromString(input string) (*MetadataWithTest, error) {
	var ret MetadataWithTest
	match := testPathRegex.FindStringSubmatch(input)
	if match == nil || len(match) != 5 {
		return &ret, fmt.Errorf("test path not in org/repo@branch:test or org/repo@branch__variant:test format: %s", input)
	}
	ret.Org = match[1]
	ret.Repo = match[2]
	ret.Test = match[4]

	branchAndVariant := strings.Split(match[3], "__")
	ret.Branch = branchAndVariant[0]
	if len(branchAndVariant) > 1 {
		ret.Variant = branchAndVariant[1]

	}
	if ret.Branch == "" || (len(branchAndVariant) > 1 && ret.Variant == "") {
		return &ret, fmt.Errorf("test path not in org/repo@branch:test or org/repo@branch__variant:test format: %s", input)
	}

	return &ret, nil
}
