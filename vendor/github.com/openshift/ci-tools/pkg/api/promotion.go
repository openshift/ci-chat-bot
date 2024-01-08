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

	var targets []PromotionTarget
	if c.Namespace != "" {
		targets = append(targets, PromotionTarget{
			Name:             c.Name,
			Namespace:        c.Namespace,
			Tag:              c.Tag,
			TagByCommit:      c.TagByCommit,
			ExcludedImages:   c.ExcludedImages,
			AdditionalImages: c.AdditionalImages,
			Disabled:         c.Disabled,
		})
	}
	targets = append(targets, c.Targets...)
	return targets
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

func tagsInQuay(image string, tag ImageStreamTagReference, date string) ([]string, error) {
	if date == "" {
		return nil, fmt.Errorf("date must not be empty")
	}
	splits := strings.Split(image, "@sha256:")
	if len(splits) != 2 {
		return nil, fmt.Errorf("malformed image pull spec: %s", image)
	}
	digest := splits[1]
	return []string{
		QuayImageFromDateAndDigest(date, digest),
		QuayImage(tag),
	}, nil
}

// QuayImage returns the image in quay.io for an image stream tag
func QuayImage(tag ImageStreamTagReference) string {
	return fmt.Sprintf("%s:%s_%s_%s", QuayOpenShiftCIRepo, tag.Namespace, tag.Name, tag.Tag)
}

// QuayImageFromDateAndDigest returns the image in quay.io for a date and an image digest
func QuayImageFromDateAndDigest(date, digest string) string {
	return fmt.Sprintf("%s:%s_sha256_%s", QuayOpenShiftCIRepo, date, digest)
}

var (
	// DefaultMirrorFunc is the default mirroring function
	DefaultMirrorFunc = func(source, target string, _ ImageStreamTagReference, _ string, mirror map[string]string) {
		mirror[target] = source
	}
	// QuayMirrorFunc is the mirroring function for quay.io
	QuayMirrorFunc = func(source, target string, tag ImageStreamTagReference, date string, mirror map[string]string) {
		if quayTags, err := tagsInQuay(source, tag, date); err != nil {
			logrus.WithField("source", source).WithError(err).
				Warn("Failed to get the tag in quay.io and skipped the promotion to quay for this image")
		} else {
			for _, quayTag := range quayTags {
				mirror[quayTag] = source
			}
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
