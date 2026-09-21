package api

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

type OKDInclusion bool

const (
	okdPromotionNamespace     = "origin"
	ocpPromotionNamespace     = "ocp"
	ocpPrivPromotionNamespace = "ocp-priv"

	WithOKD    OKDInclusion = true
	WithoutOKD OKDInclusion = false

	PromotionStepName     = "promotion"
	PromotionQuayStepName = "promotion-quay"

	PromotionExcludeImageWildcard = "*"
)

// PromotionTargets adapts the single-target configuration to the multi-target paradigm.
// This function will be removed when the previous implementation is removed.
func PromotionTargets(c *PromotionConfiguration) []PromotionTarget {
	if c == nil {
		return nil
	}
	return c.Targets
}

// ImageTargets returns image targets
func ImageTargets(c *ReleaseBuildConfiguration) sets.Set[string] {
	imageTargets := sets.New[string]()
	for _, target := range PromotionTargets(c.PromotionConfiguration) {
		for additional := range target.AdditionalImages {
			imageTargets.Insert(target.AdditionalImages[additional])
		}
	}

	if len(c.Images.Items) > 0 || imageTargets.Len() > 0 {
		imageTargets.Insert("[images]")
	}
	return imageTargets
}

// PromotesOfficialImages determines if a configuration will result in official images
// being promoted. This is a proxy for determining if a configuration contributes to
// the release payload.
func PromotesOfficialImages(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion) bool {
	for _, target := range PromotionTargets(configSpec.PromotionConfiguration) {
		if !target.Disabled && BuildsOfficialImages(target, includeOKD) {
			return true
		}
	}
	return false
}

// PromotesOfficialImage determines if a configuration will promote promotionName
// and if it belongs to any official stream.
func PromotesOfficialImage(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion, promotionName string) bool {
	for _, target := range PromotionTargets(configSpec.PromotionConfiguration) {
		if !target.Disabled && BuildsOfficialImages(target, includeOKD) && target.Name == promotionName {
			return true
		}
	}
	return false
}

// TargetsOfficialImage determines if a configuration targets promotionName in an
// official stream, regardless of whether promotion is disabled. Use this instead
// of PromotesOfficialImage when the disabled flag is irrelevant, e.g. for branch
// forwarding where release branches always have promotion disabled.
func TargetsOfficialImage(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion, promotionName string) bool {
	for _, target := range PromotionTargets(configSpec.PromotionConfiguration) {
		if BuildsOfficialImages(target, includeOKD) && target.Name == promotionName {
			return true
		}
	}
	return false
}

// BuildsOfficialImages determines if a configuration will result in official images
// being built.
func BuildsOfficialImages(configSpec PromotionTarget, includeOKD OKDInclusion) bool {
	return RefersToOfficialImage(configSpec.Namespace, includeOKD)
}

// BuildsAnyOfficialImages determines if a configuration will result in official images
// being built.
func BuildsAnyOfficialImages(configSpec *ReleaseBuildConfiguration, includeOKD OKDInclusion) bool {
	var buildsAny bool
	for _, target := range PromotionTargets(configSpec.PromotionConfiguration) {
		buildsAny = buildsAny || BuildsOfficialImages(target, includeOKD)
	}
	return buildsAny
}

// RefersToOfficialImage determines if an image is official
func RefersToOfficialImage(namespace string, includeOKD OKDInclusion) bool {
	return (bool(includeOKD) && namespace == okdPromotionNamespace) || namespace == ocpPromotionNamespace
}

// RefersToQuayReferenceImage is true for namespaces that get app.ci IS source-refs to QCI.
func RefersToQuayReferenceImage(namespace string, includeOKD OKDInclusion) bool {
	return RefersToOfficialImage(namespace, includeOKD) || namespace == ocpPrivPromotionNamespace
}

// UsableImageStreamTagImportSource reports whether ref may be copied into a release snapshot tag From.
func UsableImageStreamTagImportSource(from *coreapi.ObjectReference) bool {
	if from == nil || from.Name == "" {
		return false
	}
	switch from.Kind {
	case "DockerImage":
		return !isInternalAPPCIRegistryReference(from.Name)
	case "ImageStreamTag", "ImageStreamImage":
		return !officialPayloadNamespaces.Has(from.Namespace)
	default:
		return false
	}
}

var officialPayloadNamespaces = sets.New[string](ocpPromotionNamespace, ocpPrivPromotionNamespace, okdPromotionNamespace)

func isInternalAPPCIRegistryReference(name string) bool {
	for _, ns := range []string{ocpPromotionNamespace, ocpPrivPromotionNamespace, okdPromotionNamespace} {
		if strings.HasPrefix(name, ServiceDomainAPPCIRegistry+"/"+ns+"/") {
			return true
		}
	}
	return false
}

// QuayImage returns the image in quay.io for an image stream tag which is used to push the image
func QuayImage(tag ImageStreamTagReference) string {
	return fmt.Sprintf("%s:%s_%s_%s", QuayOpenShiftCIRepo, tag.Namespace, tag.Name, tag.Tag)
}

// QuayImageReference returns the image in quay.io for an image stream tag which is used to pull the image
func QuayImageReference(tag ImageStreamTagReference) string {
	return strings.Replace(QuayImage(tag), "quay.io", QCIAPPCIDomain, 1)
}

// quayImageWithTime returns the image in quay.io with a timestamp
func quayImageWithTime(timestamp string, tag ImageStreamTagReference) string {
	return fmt.Sprintf("%s:%s_prune_%s_%s_%s", QuayOpenShiftCIRepo, timestamp, tag.Namespace, tag.Name, tag.Tag)
}

// getQuayProxyTarget creates the quay-proxy app.ci imagestream tag reference.
// Format: namespace/imagestream-name:tag (e.g. ocp/4.22:ovn-kubernetes).
func getQuayProxyTarget(target string, tag ImageStreamTagReference) string {
	if tag.Name != "" {
		return fmt.Sprintf("%s/%s:%s", tag.Namespace, tag.Name, tag.Tag)
	}

	// For tag-based promotion, parse the target string to extract component name
	targetParts := strings.Split(target, ":")
	if len(targetParts) >= 2 && strings.HasPrefix(target, QuayOpenShiftCIRepo+":") {
		tagPart := targetParts[1]
		first := strings.Index(tagPart, "_")
		tagSuffix := "_" + tag.Tag
		if first > 0 && strings.HasSuffix(tagPart, tagSuffix) {
			targetNamespace := tagPart[:first]
			tagStart := len(tagPart) - len(tagSuffix)
			targetComponent := tagPart[first+1 : tagStart]
			if targetComponent != "" {
				return fmt.Sprintf("%s/%s:%s", targetNamespace, targetComponent, tag.Tag)
			}
		}
		if first > 0 && tag.Tag != "" {
			targetNamespace := tagPart[:first]
			remainder := tagPart[first+1:]
			if componentPrefix := tag.Tag + "_"; strings.HasPrefix(remainder, componentPrefix) {
				return fmt.Sprintf("%s/%s:%s", targetNamespace, tag.Tag, remainder[len(componentPrefix):])
			}
		}
	}

	return fmt.Sprintf("%s/%s:%s", tag.Namespace, tag.Tag, tag.Tag)
}

func qciPullSpec(pipelineSource string) (string, bool) {
	idx := strings.LastIndex(pipelineSource, "@sha256:")
	if idx == -1 {
		return "", false
	}
	digest := pipelineSource[idx+1:]
	return fmt.Sprintf("%s/openshift/ci@%s", QCIAPPCIDomain, digest), true
}

var (
	// DefaultMirrorFunc is the default mirroring function
	DefaultMirrorFunc = func(source, target string, _ ImageStreamTagReference, _ string, mirror map[string]string) {
		mirror[target] = source
	}
	// QuayMirrorFunc is the mirroring function for quay.io
	QuayMirrorFunc = func(source, target string, tag ImageStreamTagReference, time string, mirror map[string]string) {
		if time == "" {
			logrus.Warn("Found time is empty string and skipped the promotion to quay for this image")
		} else {
			t := QuayImage(tag)
			mirror[t] = source
			mirror[quayImageWithTime(time, tag)] = t
		}
	}

	// DefaultTargetNameFunc is the default target name function
	DefaultTargetNameFunc = func(registry string, config PromotionTarget) string {
		if len(config.Name) > 0 {
			return fmt.Sprintf("%s/%s/%s:${component}", registry, config.Namespace, config.Name)
		}
		return fmt.Sprintf("%s/%s/${component}:%s", registry, config.Namespace, config.Tag)
	}

	// QuayTargetNameFunc is the target name function for quay.io
	QuayTargetNameFunc = func(_ string, config PromotionTarget) string {
		if len(config.Name) > 0 {
			return fmt.Sprintf("%s:%s_%s_${component}", QuayOpenShiftCIRepo, config.Namespace, config.Name)
		}
		return fmt.Sprintf("%s:%s_${component}_%s", QuayOpenShiftCIRepo, config.Namespace, config.Tag)
	}

	// QuayCombinedMirrorFunc does both quay mirroring and quay-proxy tagging
	QuayCombinedMirrorFunc = func(source, target string, tag ImageStreamTagReference, time string, mirror map[string]string) {
		if time == "" {
			logrus.Warn("Found time is empty string and skipped the promotion to quay for this image")
		} else {
			t := QuayImage(tag)
			mirror[t] = source
			mirror[quayImageWithTime(time, tag)] = t
		}

		if !RefersToQuayReferenceImage(tag.Namespace, WithOKD) {
			return
		}
		proxyTarget := getQuayProxyTarget(target, tag)
		mirror[proxyTarget] = source
		if proxySrc, ok := qciPullSpec(source); ok {
			mirror[proxyTarget] = proxySrc
		}
	}
)
