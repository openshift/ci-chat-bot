package api

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

type OKDInclusion bool

const (
	okdPromotionNamespace = "origin"
	ocpPromotionNamespace = "ocp"

	WithOKD    OKDInclusion = true
	WithoutOKD OKDInclusion = false

	PromotionStepName     = "promotion"
	PromotionQuayStepName = "promotion-quay"
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

	if len(c.Images) > 0 || imageTargets.Len() > 0 {
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
)
