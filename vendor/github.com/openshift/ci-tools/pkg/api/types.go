package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/repoowners"

	imagev1 "github.com/openshift/api/image/v1"
)

const (
	// PromotionJobLabelKey marks promotion jobs as such. Only its presence is
	// relevant, its value is not.
	PromotionJobLabelKey = "ci-operator.openshift.io/is-promotion"
)

// IsPromotionJob determines if a given ProwJob is a PromotionJob
func IsPromotionJob(jobLabels map[string]string) bool {
	_, ok := jobLabels[PromotionJobLabelKey]
	return ok
}

// ReleaseBuildConfiguration describes how release
// artifacts are built from a repository of source
// code. The configuration is made up of two parts:
//   - minimal fields that allow the user to buy into
//     our normal conventions without worrying about
//     how the pipeline flows. Use these preferentially
//     for new projects with simple/conventional build
//     configurations.
//   - raw steps that can be used to create custom and
//     fine-grained build flows
type ReleaseBuildConfiguration struct {
	Metadata Metadata `json:"zz_generated_metadata"`

	InputConfiguration `json:",inline"`

	// BinaryBuildCommands will create a "bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "bin" image will be created.
	BinaryBuildCommands string `json:"binary_build_commands,omitempty"`

	// BinaryBuildCommandsList entries will create a "bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "bin" image will be created.
	// Mutually exclusive with BinaryBuildCommands
	// DO NOT set this in the config
	BinaryBuildCommandsList []RefCommands `json:"binary_build_commands_list,omitempty"`

	// TestBinaryBuildCommands will create a "test-bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "test-bin" image will be created.
	TestBinaryBuildCommands string `json:"test_binary_build_commands,omitempty"`

	// TestBinaryBuildCommandsList entries will create a "test-bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "test-bin" image will be created.
	// Mutually exclusive with TestBinaryBuildCommands
	// DO NOT set this in the config
	TestBinaryBuildCommandsList []RefCommands `json:"test_binary_build_commands_list,omitempty"`

	// RpmBuildCommands will create an "rpms" image from "bin" (or "src", if no
	// binary build commands were specified) that contains the output of this
	// command. The created RPMs will then be served via HTTP to the "base" image
	// via an injected rpm.repo in the standard location at /etc/yum.repos.d.
	RpmBuildCommands string `json:"rpm_build_commands,omitempty"`

	// RpmBuildCommandsList entries will create an "rpms" image from "bin" (or "src", if no
	// binary build commands were specified) that contains the output of this
	// command. The created RPMs will then be served via HTTP to the "base" image
	// via an injected rpm.repo in the standard location at /etc/yum.repos.d.
	// Mutually exclusive with RpmBuildCommands
	// DO NOT set this in the config
	RpmBuildCommandsList []RefCommands `json:"rpm_build_commands_list,omitempty"`

	// RpmBuildLocation is where RPms are deposited after being built. If
	// unset, this will default under the repository root to
	// _output/local/releases/rpms/.
	RpmBuildLocation string `json:"rpm_build_location,omitempty"`

	// RpmBuildLocationList entries are where RPms are deposited after being built. If
	// unset, this will default under the repository root to
	// _output/local/releases/rpms/.
	// Mutually exclusive with RpmBuildLocation
	// DO NOT set this in the config
	RpmBuildLocationList []RefLocation `json:"rpm_build_location_list,omitempty"`

	// CanonicalGoRepository is a directory path that represents
	// the desired location of the contents of this repository in
	// Go. If specified the location of the repository we are
	// cloning from is ignored.
	CanonicalGoRepository *string `json:"canonical_go_repository,omitempty"`

	// CanonicalGoRepositoryList is a directory path that represents
	// the desired location of the contents of this repository in
	// Go. If specified the location of the repository we are
	// cloning from is ignored.
	// Mutually exclusive with CanonicalGoRepository
	// DO NOT set this in the config
	CanonicalGoRepositoryList []RefRepository `json:"canonical_go_repository_list,omitempty"`

	// Images describes the images that are built
	// baseImage the project as part of the release
	// process. The name of each image is its "to" value
	// and can be used to build only a specific image.
	Images ImageConfiguration `json:"images,omitzero"`

	// Operator describes the operator bundle(s) that is built by the project
	Operator *OperatorStepConfiguration `json:"operator,omitempty"`

	// Tests describes the tests to run inside of built images.
	// The images launched as pods but have no explicit access to
	// the cluster they are running on.
	Tests []TestStepConfiguration `json:"tests,omitempty"`

	// RawSteps are literal Steps that should be
	// included in the final pipeline.
	RawSteps []StepConfiguration `json:"raw_steps,omitempty"`

	// PromotionConfiguration determines how images are promoted
	// by this command. It is ignored unless promotion has specifically
	// been requested. Promotion is performed after all other steps
	// have been completed so that tests can be run prior to promotion.
	// If no promotion is defined, it is defaulted from the ReleaseTagConfiguration.
	PromotionConfiguration *PromotionConfiguration `json:"promotion,omitempty"`

	// Resources is a set of resource requests or limits over the
	// input types. The special name '*' may be used to set default
	// requests and limits.
	Resources ResourceConfiguration `json:"resources,omitempty"`
}

// RefCommands pairs a ref (in org/repo format) with commands
type RefCommands struct {
	Ref      string `json:"ref"`
	Commands string `json:"commands"`
}

// RefLocation pairs a ref (in org/repo format) with a location
type RefLocation struct {
	Ref      string `json:"ref"`
	Location string `json:"location"`
}

// RefRepository pairs a ref (in org/repo format) with a repository
type RefRepository struct {
	Ref        string `json:"ref"`
	Repository string `json:"repository"`
}

// Metadata describes the source repo for which a config is written
type Metadata struct {
	Org     string `json:"org"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Variant string `json:"variant,omitempty"`
}

// BuildsImage checks if an image is built by the release configuration.
func (config ReleaseBuildConfiguration) BuildsImage(name string) bool {
	for _, i := range config.Images.Items {
		if string(i.To) == name {
			return true
		}
	}
	return false
}

// IsBaseImage checks if `name` will be a tag in the pipeline image stream
// by virtue of being imported as a base image
func (config ReleaseBuildConfiguration) IsBaseImage(name string) bool {
	return sets.KeySet(config.BaseImages).
		Union(sets.KeySet(config.BaseRPMImages)).
		Union(sets.KeySet(config.ExternalImages)).
		Has(name)
}

// IsPipelineImage checks if `name` will be a tag in the pipeline image stream.
func (config ReleaseBuildConfiguration) IsPipelineImage(name string) bool {
	if config.IsBaseImage(name) {
		return true
	}
	if strings.HasPrefix(name, string(PipelineImageStreamTagReferenceRoot)) ||
		strings.HasPrefix(name, string(PipelineImageStreamTagReferenceSource)) ||
		strings.HasPrefix(name, string(PipelineImageStreamTagReferenceBinaries)) ||
		strings.HasPrefix(name, string(PipelineImageStreamTagReferenceTestBinaries)) ||
		strings.HasPrefix(name, string(PipelineImageStreamTagReferenceRPMs)) ||
		strings.HasPrefix(name, string(PipelineImageStreamTagReferenceBundleSource)) {
		return true
	}
	if IsIndexImage(name) {
		return true
	}
	return config.IsBundleImage(name)
}

// DeterminePathAlias searches through the CanonicalGoRepositoryList to find the matching alias for the provided org and repo.
// If not found, it returns the CanonicalGoRepository if one is configured
func (config ReleaseBuildConfiguration) DeterminePathAlias(org, repo string) string {
	orgRepo := fmt.Sprintf("%s.%s", org, repo)
	for _, cgr := range config.CanonicalGoRepositoryList {
		if cgr.Ref == orgRepo {
			return cgr.Repository
		}
	}

	if config.CanonicalGoRepository != nil {
		return *config.CanonicalGoRepository
	}

	return ""
}

// ResourceConfiguration defines resource overrides for jobs run
// by the operator.
type ResourceConfiguration map[string]ResourceRequirements

func (c ResourceConfiguration) RequirementsForStep(name string) ResourceRequirements {
	req := ResourceRequirements{
		Requests: make(ResourceList),
		Limits:   make(ResourceList),
	}
	if defaults, ok := c["*"]; ok {
		req.Requests.Add(defaults.Requests)
		req.Limits.Add(defaults.Limits)
	}
	if values, ok := c[name]; ok {
		req.Requests.Add(values.Requests)
		req.Limits.Add(values.Limits)
	}
	return req
}

// ResourceRequirements are resource requests and limits applied
// to the individual steps in the job. They are passed directly to
// builds or pods.
type ResourceRequirements struct {
	// Requests are resource requests applied to an individual step in the job.
	// These are directly used in creating the Pods that execute the Job.
	Requests ResourceList `json:"requests,omitempty"`
	// Limits are resource limits applied to an individual step in the job.
	// These are directly used in creating the Pods that execute the Job.
	Limits ResourceList `json:"limits,omitempty"`
}

// ResourceList is a map of string resource names and resource
// quantities, as defined on Kubernetes objects. Common resources
// to request or limit are `cpu` and `memory`. For `cpu`, values
// are provided in vCPUs - for instance, `2` or `200m`. For
// `memory`, values are provided in bytes - for instance, `20Mi`
// or `3Gi`.
type ResourceList map[string]string

func (l ResourceList) Add(values ResourceList) {
	for name, value := range values {
		l[name] = value
	}
}

// InputConfiguration contains the set of image inputs
// to a build and can be used as an override to the
// canonical inputs by a local process.
type InputConfiguration struct {
	// The list of base images describe
	// which images are going to be necessary outside
	// of the pipeline. The key will be the alias that other
	// steps use to refer to this image.
	BaseImages map[string]ImageStreamTagReference `json:"base_images,omitempty"`
	// BaseRPMImages is a list of the images and their aliases that will
	// have RPM repositories injected into them for downstream
	// image builds that require built project RPMs.
	BaseRPMImages map[string]ImageStreamTagReference `json:"base_rpm_images,omitempty"`

	// BuildRootImage supports two ways to get the image that
	// the pipeline will caches on. The one way is to take the reference
	// from an image stream, and the other from a dockerfile.
	BuildRootImage *BuildRootImageConfiguration `json:"build_root,omitempty"`

	// BuildRootImages entries support two ways to get the image that
	// the pipeline will caches on. The one way is to take the reference
	// from an image stream, and the other from a dockerfile.
	// Mutually exclusive with BuildRootImage
	// DO NOT set this in the config
	BuildRootImages map[string]BuildRootImageConfiguration `json:"build_roots,omitempty"`

	// ExternalImages are images that are imported into the pipeline from an external source.
	ExternalImages map[string]ExternalImage `json:"external_images,omitempty"`

	// ReleaseTagConfiguration determines how the
	// full release is assembled.
	ReleaseTagConfiguration *ReleaseTagConfiguration `json:"tag_specification,omitempty"`

	// Releases maps semantic release payload identifiers
	// to the names that they will be exposed under. For
	// instance, an 'initial' name will be exposed as
	// $RELEASE_IMAGE_INITIAL. The 'latest' key is special
	// and cannot co-exist with 'tag_specification', as
	// they result in the same output.
	Releases map[string]UnresolvedRelease `json:"releases,omitempty"`
}

// ExternalImage describes the external image that is imported into the pipeline
type ExternalImage struct {
	// Registry is the registry to pull images from (e.g. quay.io)
	Registry                string `json:"registry"`
	ImageStreamTagReference `json:",inline"`
	// PullSecret is the name of the secret to use to pull the image
	PullSecret string `json:"pull_secret,omitempty"`
	// PullSpec is the full pullSpec of the external image, only to be set programmatically,
	// and takes precedent over the other fields in ExternalImage
	PullSpec string `json:"pull_spec,omitempty"`
}

// UnresolvedRelease describes a semantic release payload
// identifier we need to resolve to a pull spec.
type UnresolvedRelease struct {
	// Integration describes an integration stream which we can create a payload out of
	Integration *Integration `json:"integration,omitempty"`
	// Candidate describes a candidate release payload
	Candidate *Candidate `json:"candidate,omitempty"`
	// Prerelease describes a yet-to-be released payload
	Prerelease *Prerelease `json:"prerelease,omitempty"`
	// Release describes a released payload
	Release *Release `json:"release,omitempty"`
}

// Integration is an ImageStream holding the latest images from development builds of OCP.
type Integration struct {
	// Namespace is the namespace in which the integration stream lives.
	Namespace string `json:"namespace"`
	// Name is the name of the ImageStream
	Name string `json:"name"`
	// IncludeBuiltImages determines if the release we assemble will include
	// images built during the test itself.
	IncludeBuiltImages bool `json:"include_built_images,omitempty"`
	// ReferencePolicy is the policy to use when resolving references (Local or Source)
	ReferencePolicy *imagev1.TagReferencePolicyType `json:"reference_policy,omitempty"`
}

// ReleaseDescriptor holds common data for different types of release payloads
type ReleaseDescriptor struct {
	// Product is the name of the product being released
	Product ReleaseProduct `json:"product"`
	// Architecture is the architecture for the product.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// Relative optionally specifies how old of a release
	// is requested from this stream. For instance, a value
	// of 1 will resolve to the previous validated release
	// for this stream.
	Relative int `json:"relative,omitempty"`
}

// Candidate describes a validated candidate release payload
type Candidate struct {
	ReleaseDescriptor `json:",inline"`
	// ReleaseStream is the stream from which we pick the latest candidate
	Stream ReleaseStream `json:"stream"`
	// Version is the minor version to search for
	Version string `json:"version"`
}

// Prerelease describes a validated release payload before it is exposed
type Prerelease struct {
	ReleaseDescriptor `json:",inline"`
	// VersionBounds describe the allowable version bounds to search in
	VersionBounds VersionBounds `json:"version_bounds"`
}

// VersionBounds describe the upper and lower bounds and stream on a version search
type VersionBounds struct {
	Lower string `json:"lower"`
	Upper string `json:"upper"`
	// Stream dictates which stream to search for a version within the specified bounds
	// defaults to 4-stable.
	Stream string `json:"stream,omitempty"`
}

func (b *VersionBounds) Query() string {
	return fmt.Sprintf(">%s <%s", b.Lower, b.Upper)
}

func BoundsFromQuery(query string) (*VersionBounds, error) {
	splitParts := strings.Split(query, " ")
	if len(splitParts) != 2 || !strings.HasPrefix(splitParts[0], ">") || !strings.HasPrefix(splitParts[1], "<") {
		return nil, fmt.Errorf("Invalid version range `%s`. Must be in form `>4.x.y <4.a.b-c`", query)

	}
	return &VersionBounds{
		Lower: strings.TrimPrefix(splitParts[0], ">"),
		Upper: strings.TrimPrefix(splitParts[1], "<"),
	}, nil
}

// ReleaseProduct describes the product being released
type ReleaseProduct string

const (
	ReleaseProductOCP     ReleaseProduct = "ocp"
	ReleaseProductOKD     ReleaseProduct = "okd"
	ReleaseProductOKDScos ReleaseProduct = "okd-scos"
)

// ReleaseArchitecture describes the architecture for the product
type ReleaseArchitecture string

const (
	ReleaseArchitectureAMD64   ReleaseArchitecture = "amd64"
	ReleaseArchitecturePPC64le ReleaseArchitecture = "ppc64le"
	ReleaseArchitectureS390x   ReleaseArchitecture = "s390x"
	ReleaseArchitectureARM64   ReleaseArchitecture = "arm64"
	ReleaseArchitectureMULTI   ReleaseArchitecture = "multi" //heterogeneous payload
)

// NodeArchitecture describes the architecture for the node
type NodeArchitecture string

func (na NodeArchitecture) Validate() error {
	switch na {
	case NodeArchitectureAMD64, NodeArchitectureARM64, NodeArchitectureS390x, NodeArchitecturePPC64le:
		return nil
	}

	return fmt.Errorf("invalid node architecture %s, expected one of %v, %v, %v or %v", na, NodeArchitectureAMD64, NodeArchitectureARM64, NodeArchitectureS390x, NodeArchitecturePPC64le)
}

const (
	NodeArchitectureAMD64   NodeArchitecture = "amd64"
	NodeArchitectureARM64   NodeArchitecture = "arm64"
	NodeArchitectureS390x   NodeArchitecture = "s390x"
	NodeArchitecturePPC64le NodeArchitecture = "ppc64le"
)

type ReleaseStream string

const (
	ReleaseStreamCI             ReleaseStream = "ci"
	ReleaseStreamNightly        ReleaseStream = "nightly"
	ReleaseStreamOKD            ReleaseStream = "okd"
	ReleaseStreamOKDScos        ReleaseStream = "okd-scos"
	ReleaseStreamKonfluxNightly ReleaseStream = "konflux-nightly"
)

// Release describes a generally available release payload
type Release struct {
	// Version is the minor version to search for
	Version string `json:"version"`
	// Channel is the release channel to search in
	Channel ReleaseChannel `json:"channel"`
	// Architecture is the architecture for the release.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// Relative optionally specifies how old of a release
	// is requested from this channel. For instance, a value
	// of 1 will resolve to the previous release in the
	// upgrade graph for this channel.  This field is ignored
	// if an explicit Version is provided.
	Relative int `json:"relative,omitempty"`
}

type ReleaseChannel string

const (
	ReleaseChannelStable    ReleaseChannel = "stable"
	ReleaseChannelFast      ReleaseChannel = "fast"
	ReleaseChannelCandidate ReleaseChannel = "candidate"

	// CIOperatorInrepoConfigFileName is the name of the file that contains the build root images
	// pullspec.
	CIOperatorInrepoConfigFileName = ".ci-operator.yaml"
)

type CIOperatorInrepoConfig struct {
	BuildRootImage ImageStreamTagReference `json:"build_root_image"`
}

// BuildRootImageConfiguration holds the two ways of using a base image
// that the pipeline will caches on.
type BuildRootImageConfiguration struct {
	ImageStreamTagReference *ImageStreamTagReference          `json:"image_stream_tag,omitempty"`
	ProjectImageBuild       *ProjectDirectoryImageBuildInputs `json:"project_image,omitempty"`
	// If the BuildRoot images pullspec should be read from a file in the repository (BuildRootImageFileName).
	FromRepository bool `json:"from_repository,omitempty"`

	// UseBuildCache enables the import and use of the prior `bin` image
	// as a build cache, if the underlying build root has not changed since
	// the previous cache was published.
	UseBuildCache bool `json:"use_build_cache,omitempty"`
}

// ImageStreamTagReference identifies an ImageStreamTag
type ImageStreamTagReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`

	// As is an optional string to use as the intermediate name for this reference.
	As string `json:"as,omitempty"`
}

func (i *ImageStreamTagReference) ISTagName() string {
	return fmt.Sprintf("%s/%s:%s", i.Namespace, i.Name, i.Tag)
}

// ReleaseTagConfiguration describes how a release is
// assembled from release artifacts. A release image stream is a
// single stream with multiple tags (openshift/origin-v3.9:control-plane),
// each tag being a unique and well defined name for a component.
type ReleaseTagConfiguration struct {
	// Namespace identifies the namespace from which
	// all release artifacts not built in the current
	// job are tagged from.
	Namespace string `json:"namespace"`

	// Name is the image stream name to use that contains all
	// component tags.
	Name string `json:"name"`

	// IncludeBuiltImages determines if the release we assemble will include
	// images built during the test itself.
	IncludeBuiltImages bool `json:"include_built_images,omitempty"`

	// ReferencePolicy is the policy to use when resolving references (Local or Source)
	ReferencePolicy *imagev1.TagReferencePolicyType `json:"reference_policy,omitempty"`
}

func (config ReleaseTagConfiguration) InputsName() string {
	return "[release-inputs]"
}

func (config ReleaseTagConfiguration) TargetName(name string) string {
	return fmt.Sprintf("[release:%s]", name)
}

// ReleaseConfiguration records a resolved release with its name.
// We always expect this step to be preempted with an env var
// that was set at startup. This will be cleaner when we refactor
// release dependencies.
type ReleaseConfiguration struct {
	Name              string `json:"name"`
	UnresolvedRelease `json:",inline"`
}

func (config ReleaseConfiguration) TargetName() string {
	return fmt.Sprintf("[release:%s]", config.Name)
}

// PromotionConfiguration describes where images created by this
// config should be published to. The release tag configuration
// defines the inputs, while this defines the outputs.
type PromotionConfiguration struct {
	// Targets configure a set of images to be pushed to
	// a registry.
	Targets []PromotionTarget `json:"to,omitempty"`

	// RegistryOverride is an override for the registry domain to
	// which we will mirror images. This is an advanced option and
	// should *not* be used in common test workflows. The CI chat
	// bot uses this option to facilitate image sharing.
	RegistryOverride string `json:"registry_override,omitempty"`

	// DisableBuildCache stops us from uploading the build cache.
	// This is useful (only) for CI chat bot invocations where
	// promotion does not imply output artifacts are being created
	// for posterity.
	DisableBuildCache bool `json:"disable_build_cache,omitempty"`

	// Cron generates promotion periodic alongside with promotion
	// postsubmit
	Cron string `json:"cron,omitempty"`
}

type PromotionTarget struct {
	// Namespace identifies the namespace to which the built
	// artifacts will be published to.
	Namespace string `json:"namespace"`

	// Name is an optional image stream name to use that
	// contains all component tags. If specified, tag is
	// ignored.
	Name string `json:"name,omitempty"`

	// Tag is the ImageStreamTag tagged in for each
	// build image's ImageStream.
	Tag string `json:"tag,omitempty"`

	// TagByCommit determines if an image should be tagged by the
	// git commit that was used to build it. If Tag is also set,
	// this will cause both a floating tag and commit-specific tags
	// to be promoted.
	TagByCommit bool `json:"tag_by_commit,omitempty"`

	// ExcludedImages are image names that will not be promoted.
	// Exclusions are made before additional_images are included.
	// Use exclusions when you want to build images for testing
	// but not promote them afterwards.
	ExcludedImages []string `json:"excluded_images,omitempty"`

	// AdditionalImages is a mapping of images to promote. The
	// images will be taken from the pipeline image stream. The
	// key is the name to promote as and the value is the source
	// name. If you specify a tag that does not exist as the source
	// the destination tag will not be created.
	AdditionalImages map[string]string `json:"additional_images,omitempty"`

	// Disabled will no-op succeed instead of running the actual
	// promotion step. This is useful when two branches need to
	// promote to the same output imagestream on a cut-over but
	// never concurrently, and you want to have promotion config
	// in the ci-operator configuration files all the time.
	Disabled bool `json:"disabled,omitempty"`
}

// StepConfiguration holds one step configuration.
// Only one of the fields in this can be non-null.
type StepConfiguration struct {
	InputImageTagStepConfiguration              *InputImageTagStepConfiguration              `json:"input_image_tag_step,omitempty"`
	PipelineImageCacheStepConfiguration         *PipelineImageCacheStepConfiguration         `json:"pipeline_image_cache_step,omitempty"`
	SourceStepConfiguration                     *SourceStepConfiguration                     `json:"source_step,omitempty"`
	BundleSourceStepConfiguration               *BundleSourceStepConfiguration               `json:"bundle_source_step,omitempty"`
	IndexGeneratorStepConfiguration             *IndexGeneratorStepConfiguration             `json:"index_generator_step,omitempty"`
	ProjectDirectoryImageBuildStepConfiguration *ProjectDirectoryImageBuildStepConfiguration `json:"project_directory_image_build_step,omitempty"`
	RPMImageInjectionStepConfiguration          *RPMImageInjectionStepConfiguration          `json:"rpm_image_injection_step,omitempty"`
	RPMServeStepConfiguration                   *RPMServeStepConfiguration                   `json:"rpm_serve_step,omitempty"`
	OutputImageTagStepConfiguration             *OutputImageTagStepConfiguration             `json:"output_image_tag_step,omitempty"`
	ReleaseImagesTagStepConfiguration           *ReleaseTagConfiguration                     `json:"release_images_tag_step,omitempty"`
	ResolvedReleaseImagesStepConfiguration      *ReleaseConfiguration                        `json:"resolved_release_images_step,omitempty"`
	TestStepConfiguration                       *TestStepConfiguration                       `json:"test_step,omitempty"`
	ProjectDirectoryImageBuildInputs            *ProjectDirectoryImageBuildInputs            `json:"project_directory_image_build_inputs,omitempty"`
}

// InputImageTagStepConfiguration describes a step that
// tags an externalImage image in to the build pipeline.
// if no explicit output tag is provided, the name
// of the image is used as the tag.
type InputImageTagStepConfiguration struct {
	InputImage `json:",inline"`
	Sources    []ImageStreamSource `json:"-"`
}

func (config InputImageTagStepConfiguration) TargetName() string {
	return fmt.Sprintf("[input:%s]", config.To)
}

func (config InputImageTagStepConfiguration) Matches(other InputImage) bool {
	return config.InputImage == other
}

func (config InputImageTagStepConfiguration) FormattedSources() string {
	var formattedSources []string
	tests := sets.Set[string]{}
	for _, source := range config.Sources {
		switch source.SourceType {
		case ImageStreamSourceTest:
			tests.Insert(source.Name)
		default:
			item := string(source.SourceType)
			if source.Name != "" {
				item += ": " + source.Name
			}
			formattedSources = append(formattedSources, item)
		}
	}

	if len(tests) > 0 {
		formattedSources = append(formattedSources, fmt.Sprintf("test steps: %s", strings.Join(sets.List(tests), ",")))

	}

	return strings.Join(formattedSources, "|")

}

func (config *InputImageTagStepConfiguration) AddSources(sources ...ImageStreamSource) {
	config.Sources = append(config.Sources, sources...)
}

type InputImage struct {
	BaseImage     ImageStreamTagReference         `json:"base_image"`
	ExternalImage *ExternalImage                  `json:"external_image,omitempty"`
	To            PipelineImageStreamTagReference `json:"to,omitempty"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`
}

type ImageStreamSourceType string

const (
	ImageStreamSourceRoot     ImageStreamSourceType = "root"
	ImageStreamSourceBase     ImageStreamSourceType = "base_image"
	ImageStreamSourceExternal ImageStreamSourceType = "external_images"
	ImageStreamSourceBaseRpm  ImageStreamSourceType = "base_rpm_image"
	ImageStreamSourceTest     ImageStreamSourceType = "test step"
)

type ImageStreamSource struct {
	SourceType ImageStreamSourceType
	Name       string
}

// OutputImageTagStepConfiguration describes a step that
// tags a pipeline image out from the build pipeline.
type OutputImageTagStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   ImageStreamTagReference         `json:"to"`

	// Optional means the output step is not built, published, or
	// promoted unless explicitly targeted. Use for builds which
	// are invoked only when testing certain parts of the repo.
	Optional bool `json:"optional"`
}

func (config OutputImageTagStepConfiguration) TargetName() string {
	if len(config.To.As) == 0 {
		return fmt.Sprintf("[output:%s:%s]", config.To.Name, config.To.Tag)
	}
	return config.To.As
}

// PipelineImageCacheStepConfiguration describes a
// step that builds a container image to cache the
// output of commands.
type PipelineImageCacheStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to"`

	// Commands are the shell commands to run in
	// the repository root to create the cached
	// content.
	Commands string `json:"commands"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`
}

func (config PipelineImageCacheStepConfiguration) TargetName() string {
	return string(config.To)
}

// Cluster is the name of a cluster in CI build farm.
type Cluster string

const (
	ClusterAPPCI     Cluster = "app.ci"
	ClusterBuild01   Cluster = "build01"
	ClusterBuild02   Cluster = "build02"
	ClusterBuild03   Cluster = "build03"
	ClusterBuild10   Cluster = "build10"
	ClusterVSphere02 Cluster = "vsphere02"
	ClusterARM01     Cluster = "arm01"
	ClusterHive      Cluster = "hosted-mgmt"
)

// TestStepConfiguration describes a step that runs a
// command in one of the previously built images and then
// gathers artifacts from that step.
type TestStepConfiguration struct {
	// As is the name of the test.
	As string `json:"as"`

	// Capabilities is the list of strings that
	// define additional capabilities needed by the test runs
	Capabilities []string `json:"capabilities,omitempty"`

	// NestedPodman enables this test to run podman inside a container.
	NestedPodman bool `json:"nested_podman,omitempty"`

	// Commands are the shell commands to run in
	// the repository root to execute tests.
	Commands string `json:"commands,omitempty"`

	// Cluster specifies the name of the cluster where the test runs.
	Cluster Cluster `json:"cluster,omitempty"`

	// Secret is an optional secret object which
	// will be mounted inside the test container.
	// You cannot set the Secret and Secrets attributes
	// at the same time.
	Secret *Secret `json:"secret,omitempty"`

	// Secrets is an optional array of secret objects
	// which will be mounted inside the test container.
	// You cannot set the Secret and Secrets attributes
	// at the same time.
	Secrets []*Secret `json:"secrets,omitempty"`

	// Cron is how often the test is expected to run outside
	// of pull request workflows. Setting this field will
	// create a periodic job instead of a presubmit
	Cron *string `json:"cron,omitempty"`

	// Presubmit configures prowgen to generate a presubmit job in additional to the periodic job.
	// It can be used only when the test itself is a periodic job.
	Presubmit bool `json:"presubmit,omitempty"`

	// Interval is how frequently the test should be run based
	// on the last time the test ran. Setting this field will
	// create a periodic job instead of a presubmit
	Interval *string `json:"interval,omitempty"`

	// MinimumInterval to wait between two runs of the job. Consecutive
	// jobs are run at `minimum_interval` + `duration of previous job`
	// apart. Setting this field will create a periodic job instead of a
	// presubmit
	MinimumInterval *string `json:"minimum_interval,omitempty"`

	// ReleaseController configures prowgen to create a periodic that
	// does not get run by prow and instead is run by release-controller.
	// The job must be configured as a verification or periodic job in a
	// release-controller config file when this field is set to `true`.
	ReleaseController bool `json:"release_controller,omitempty"`

	// Postsubmit configures prowgen to generate the job as a postsubmit rather than a presubmit
	Postsubmit bool `json:"postsubmit,omitempty"`

	// ClusterClaim claims an OpenShift cluster and exposes environment variable ${KUBECONFIG} to the test container
	ClusterClaim *ClusterClaim `json:"cluster_claim,omitempty"`

	// AlwaysRun can be set to false to disable running the job on every PR
	AlwaysRun *bool `json:"always_run,omitempty"`

	// Retry is a configuration entry for retrying periodic prowjobs
	Retry *prowconfig.Retry `json:"retry,omitempty"`

	// RunIfChanged is a regex that will result in the test only running if something that matches it was changed.
	RunIfChanged string `json:"run_if_changed,omitempty"`

	// PipelineRunIfChanged is a regex that will result in the test only running in second
	// stage of the pipeline run if something that matches it was changed.
	PipelineRunIfChanged string `json:"pipeline_run_if_changed,omitempty"`

	// Optional indicates that the job's status context, that is generated from the corresponding test, should not be required for merge.
	Optional bool `json:"optional,omitempty"`

	// Portable allows to port periodic tests to current and future release despite the demand to skip periodics
	Portable bool `json:"portable,omitempty"`

	// SkipIfOnlyChanged is a regex that will result in the test being skipped if all changed files match that regex.
	SkipIfOnlyChanged string `json:"skip_if_only_changed,omitempty"`

	// PipelineSkipIfOnlyChanged is a regex that will result in the test being skipped in second
	// stage of the pipeline run if all changed files match that regex.
	PipelineSkipIfOnlyChanged string `json:"pipeline_skip_if_only_changed,omitempty"`

	// Timeout overrides maximum prowjob duration
	Timeout *prowv1.Duration `json:"timeout,omitempty"`

	// NodeArchitecture is the architecture for the node where the test will run.
	// If set, the generated test pod will include a nodeSelector for this architecture.
	NodeArchitecture NodeArchitecture `json:"node_architecture,omitempty"`

	// RestrictNetworkAccess restricts network access to RedHat intranet.
	RestrictNetworkAccess *bool `json:"restrict_network_access,omitempty"`

	// ShardCount describes the number of jobs that should be generated as shards for this test
	// Each generated job will be a duplication, but contain a suffix and the necessary SHARD_ARGS will be passed to the steps
	// Only applicable to presubmits and periodics
	ShardCount *int `json:"shard_count,omitempty"`

	// Only one of the following can be not-null.
	ContainerTestConfiguration         *ContainerTestConfiguration         `json:"container,omitempty"`
	MultiStageTestConfiguration        *MultiStageTestConfiguration        `json:"steps,omitempty"`
	MultiStageTestConfigurationLiteral *MultiStageTestConfigurationLiteral `json:"literal_steps,omitempty"`
}

func (config TestStepConfiguration) TargetName() string {
	return config.As
}

func (config TestStepConfiguration) IsPeriodic() bool {
	return config.Interval != nil || config.MinimumInterval != nil || config.Cron != nil || config.ReleaseController
}

// GetClusterProfileName returns the cluster profile name if it's set
func (config TestStepConfiguration) GetClusterProfileName() string {
	switch {
	case config.MultiStageTestConfigurationLiteral != nil:
		return config.MultiStageTestConfigurationLiteral.ClusterProfile.Name()
	case config.MultiStageTestConfiguration != nil:
		return config.MultiStageTestConfiguration.ClusterProfile.Name()
	default:
		return ""
	}
}

// Cloud is the name of a cloud provider, e.g., aws cluster topology, etc.
type Cloud string

const (
	CloudAWS     Cloud = "aws"
	CloudGCP     Cloud = "gcp"
	CloudAzure4  Cloud = "azure4"
	CloudVSphere Cloud = "vsphere"
)

// ClusterClaim claims an OpenShift cluster for the job.
type ClusterClaim struct {
	// As is the name to use when importing the cluster claim release payload.
	// If unset, claim release will be imported as `latest`.
	As string `json:"as,omitempty"`
	// Product is the name of the product being released.
	// Defaults to ocp.
	Product ReleaseProduct `json:"product,omitempty"`
	// Version is the version of the product
	Version string `json:"version"`
	// Architecture is the architecture for the product.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// Cloud is the cloud where the product is installed, e.g., aws.
	Cloud Cloud `json:"cloud"`
	// Owner is the owner of cloud account used to install the product, e.g., dpp.
	Owner string `json:"owner"`
	// Labels is the labels to select the cluster pools
	Labels map[string]string `json:"labels,omitempty"`
	// Timeout is how long ci-operator will wait for the cluster to be ready.
	// Defaults to 1h.
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
}

type ClaimRelease struct {
	ReleaseName  string
	OverrideName string
}

func (c *ClusterClaim) ClaimRelease(testName string) *ClaimRelease {
	var as string
	if c.As == "" {
		as = LatestReleaseName
	} else {
		as = c.As
	}
	return &ClaimRelease{
		ReleaseName:  fmt.Sprintf("%s-%s", as, testName),
		OverrideName: as,
	}
}

// RegistryReferenceConfig is the struct that step references are unmarshalled into.
type RegistryReferenceConfig struct {
	// Reference is the top level field of a reference config.
	Reference RegistryReference `json:"ref,omitempty"`
}

// RegistryReference contains the LiteralTestStep of a reference as well as the documentation for the step.
type RegistryReference struct {
	// LiteralTestStep defines the full test step that can be run by the ci-operator.
	LiteralTestStep `json:",inline"`
	// Documentation describes what the step being referenced does.
	Documentation string `json:"documentation,omitempty"`
}

// RegistryChainConfig is the struct that chain references are unmarshalled into.
type RegistryChainConfig struct {
	// Chain is the top level field of a chain config.
	Chain RegistryChain `json:"chain,omitempty"`
}

// RegistryChain contains the array of steps, name, and documentation for a step chain.
type RegistryChain struct {
	// As defines the name of the chain. This is how the chain will be referenced from a job's config.
	As string `json:"as,omitempty"`
	// Steps contains the list of steps that comprise the chain. Steps will be run in the order they are defined.
	Steps []TestStep `json:"steps"`
	// Documentation describes what the chain does.
	Documentation string `json:"documentation,omitempty"`
	// Environment lists parameters that should be set by the test.
	Environment []StepParameter `json:"env,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
}

// RegistryWorkflowConfig is the struct that workflow references are unmarshalled into.
type RegistryWorkflowConfig struct {
	// Workflow is the top level field of a workflow config.
	Workflow RegistryWorkflow `json:"workflow,omitempty"`
}

// RegistryWorkflow contains the MultiStageTestConfiguration, name, and documentation for a workflow.
type RegistryWorkflow struct {
	// As defines the name of the workflow. This is how the workflow will be referenced from a job's config.
	As string `json:"as,omitempty"`
	// Steps contains the MultiStageTestConfiguration that the workflow defines.
	Steps MultiStageTestConfiguration `json:"steps,omitempty"`
	// Documentation describes what the workflow does.
	Documentation string `json:"documentation,omitempty"`
}

// RegistryObserverConfig is the struct that observer configs are unmarshalled into
type RegistryObserverConfig struct {
	// Observer is the top level field of an observer config
	Observer RegistryObserver `json:"observer,omitempty"`
}

// RegistryObserver contains the configuration and documentation for an observer
type RegistryObserver struct {
	// Observer defines the observer pod
	Observer `json:",inline"`
	// Documentation describes what the observer being configured does.
	Documentation string `json:"documentation,omitempty"`
}

// RegistryMetadata maps the registry info for each step in the registry by filename
// +k8s:deepcopy-gen=false
type RegistryMetadata map[string]RegistryInfo

// RegistryInfo contains metadata about a registry component that is useful for the web UI of the step registry
// +k8s:deepcopy-gen=false
type RegistryInfo struct {
	// Path is the path of the directoryfor the registry component relative to the registry's base directory
	Path string `json:"path,omitempty"`
	// Owners is the OWNERS config for the registry component
	Owners repoowners.Config `json:"owners,omitempty"`
}

// Observer is the configuration for an observer Pod that will run in parallel
// with a multi-stage test job.
type Observer struct {
	// Name is the name of this observer
	Name string `json:"name"`
	// From is the container image that will be used for this observer.
	From string `json:"from,omitempty"`
	// FromImage is a literal ImageStreamTag reference to use for this observer.
	FromImage *ImageStreamTagReference `json:"from_image,omitempty"`
	// Commands is the command(s) that will be run inside the image.
	Commands string `json:"commands,omitempty"`
	// Resources defines the resource requirements for the step.
	Resources ResourceRequirements `json:"resources,omitempty"`
	// Timeout is how long the we will wait before aborting a job with SIGINT.
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
	// GracePeriod is how long the we will wait after sending SIGINT to send
	// SIGKILL when aborting this observer.
	GracePeriod *prowv1.Duration `json:"grace_period,omitempty"`
	// Environment has the values of parameters for the observer.
	Environment []StepParameter `json:"env,omitempty"`
}

// Observers is a configuration for which observer pods should and should not
// be run during a job
type Observers struct {
	// Enable is a list of named observer that should be enabled
	Enable []string `json:"enable,omitempty"`
	// Disable is a list of named observers that should be disabled
	Disable []string `json:"disable,omitempty"`
}

// LiteralTestStep is the external representation of a test step allowing users
// to define new test steps. It gets converted to an internal LiteralTestStep
// struct that represents the full configuration that ci-operator can use.
type LiteralTestStep struct {
	// As is the name of the LiteralTestStep.
	As string `json:"as,omitempty"`
	// From is the container image that will be used for this step.
	From string `json:"from,omitempty"`
	// FromImage is a literal ImageStreamTag reference to use for this step.
	FromImage *ImageStreamTagReference `json:"from_image,omitempty"`
	// NestedPodman enables this test to run podman inside a container.
	NestedPodman bool `json:"nested_podman,omitempty"`
	// Commands is the command(s) that will be run inside the image.
	Commands string `json:"commands,omitempty"`
	// Resources defines the resource requirements for the step.
	Resources ResourceRequirements `json:"resources"`
	// Timeout is how long the we will wait before aborting a job with SIGINT.
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
	// GracePeriod is how long the we will wait after sending SIGINT to send
	// SIGKILL when aborting a Step.
	GracePeriod *prowv1.Duration `json:"grace_period,omitempty"`
	// Credentials defines the credentials we'll mount into this step.
	Credentials []CredentialReference `json:"credentials,omitempty"`
	// Environment lists parameters that should be set by the test.
	Environment []StepParameter `json:"env,omitempty"`
	// Dependencies lists images which must be available before the test runs
	// and the environment variables which are used to expose their pull specs.
	Dependencies []StepDependency `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// OptionalOnSuccess defines if this step should be skipped as long
	// as all `pre` and `test` steps were successful and AllowSkipOnSuccess
	// flag is set to true in MultiStageTestConfiguration. This option is
	// applicable to `post` steps.
	OptionalOnSuccess *bool `json:"optional_on_success,omitempty"`
	// BestEffort defines if this step should cause the job to fail when the
	// step fails. This only applies when AllowBestEffortPostSteps flag is set
	// to true in MultiStageTestConfiguration. This option is applicable to
	// `post` steps.
	BestEffort *bool `json:"best_effort,omitempty"`
	// NoKubeconfig determines that no $KUBECONFIG will exist in $SHARED_DIR,
	// so no local copy of it will be created for the step and if the step
	// creates one, it will not be propagated.
	NoKubeconfig *bool `json:"no_kubeconfig,omitempty"`
	// Cli is the (optional) name of the release from which the `oc` binary
	// will be injected into this step.
	Cli string `json:"cli,omitempty"`
	// Observers are the observers that should be running
	Observers []string `json:"observers,omitempty"`
	// RunAsScript defines if this step should be executed as a script mounted
	// in the test container instead of being executed directly via bash
	RunAsScript *bool `json:"run_as_script,omitempty"`
	// NodeArchitecture is the architecture for the node where the test will run.
	// If set, the generated test pod will include a nodeSelector for this architecture.
	NodeArchitecture *NodeArchitecture `json:"node_architecture,omitempty"`
}

// StepParameter is a variable set by the test, with an optional default.
type StepParameter struct {
	// Name of the environment variable.
	Name string `json:"name"`
	// Default if not set, optional, makes the parameter not required if set.
	Default *string `json:"default,omitempty"`
	// Documentation is a textual description of the parameter.
	Documentation string `json:"documentation,omitempty"`
}

// CredentialReference defines a secret to mount into a step and where to mount it.
type CredentialReference struct {
	// As is an optional string under which the secret will be stored on the file system.
	As string `json:"as,omitempty"`
	// Bundle is a named bundle reference from the GSM config mapping file.
	// Mutually exclusive with Collection/Group/Field.
	Bundle string `json:"bundle,omitempty"`
	// Collection is the name of the collection the secret belongs to (first level of 3-level <collection>__<group>__<field> hierarchy).
	Collection string `json:"collection,omitempty"`
	// Field is the specific field name (third level of 3-level <collection>__<group>__<field> hierarchy).
	// If omitted, all fields in the collection/group are auto-discovered.
	Field string `json:"field,omitempty"`
	// Group is the group name within the collection (second level of 3-level <collection>__<group>__<field> hierarchy).
	// Required when Collection is set, unless Bundle is used.
	Group string `json:"group,omitempty"`
	// MountPath is where the secret should be mounted.
	MountPath string `json:"mount_path"`
	// Namespace is where the source secret exists.
	Namespace string `json:"namespace"`
	// Name is the name of the secret.
	Name string `json:"name,omitempty"`
}

func (c *CredentialReference) IsAutoDiscovery() bool {
	return c.Field == "" && c.Group != "" && c.Collection != ""
}

func (c *CredentialReference) IsExplicitField() bool {
	return c.Field != "" && c.Group != "" && c.Collection != ""
}

func (c *CredentialReference) IsBundleReference() bool {
	return c.Bundle != ""
}

// StepDependency defines a dependency on an image and the environment variable
// used to expose the image's pull spec to the step.
type StepDependency struct {
	// Name is the tag or stream:tag that this dependency references
	Name string `json:"name"`
	// Env is the environment variable that the image's pull spec is exposed with
	Env string `json:"env"`
	// PullSpec allows the ci-operator user to pass in an external pull-spec that should be used when resolving the dependency
	PullSpec string `json:"-"`
}

// StepDNSConfig defines a resource that needs to be acquired prior to execution.
// Used to expose to the step via the specificed search list
type StepDNSConfig struct {
	// Nameservers is a list of IP addresses that will be used as DNS servers for the Pod
	Nameservers []string `json:"nameservers,omitempty"`
	// Searches is a list of DNS search domains for host-name lookup
	Searches []string `json:"searches,omitempty"`
}

// StepLease defines a resource that needs to be acquired prior to execution.
// The resource name will be exposed to the step via the specificed environment
// variable.
type StepLease struct {
	// ResourceType is the type of resource that will be leased.
	ResourceType string `json:"resource_type"`
	// Env is the environment variable that will contain the resource name.
	Env string `json:"env"`
	// Count is the number of resources to acquire (optional, defaults to 1).
	Count                uint   `json:"count,omitempty"`
	ClusterProfile       string `json:"-"`
	ClusterProfileTarget string `json:"-"`
}

// FromImageTag returns the internal name for the image tag that will be used
// for this step, if one is configured.
func (s *LiteralTestStep) FromImageTag() (PipelineImageStreamTagReference, bool) {
	if s.FromImage == nil {
		return "", false
	}
	return PipelineImageStreamTagReference(fmt.Sprintf("%s-%s-%s", s.FromImage.Namespace, s.FromImage.Name, s.FromImage.Tag)), true
}

// TestStep is the struct that a user's configuration gets unmarshalled into.
// It can contain either a LiteralTestStep, Reference, or Chain. If more than one is filled in an
// the same time, config validation will fail.
type TestStep struct {
	// LiteralTestStep is a full test step definition.
	*LiteralTestStep `json:",inline,omitempty"`
	// Reference is the name of a step reference.
	Reference *string `json:"ref,omitempty"`
	// Chain is the name of a step chain reference.
	Chain *string `json:"chain,omitempty"`
}

// MultiStageTestConfiguration is a flexible configuration mode that allows tighter control over
// the multiple stages of end to end tests.
type MultiStageTestConfiguration struct {
	// ClusterProfile defines the profile/cloud provider for end-to-end test steps.
	ClusterProfile ClusterProfile `json:"cluster_profile,omitempty"`
	// Pre is the array of test steps run to set up the environment for the test.
	Pre []TestStep `json:"pre,omitempty"`
	// Test is the array of test steps that define the actual test.
	Test []TestStep `json:"test,omitempty"`
	// Post is the array of test steps run after the tests finish and teardown/deprovision resources.
	// Post steps always run, even if previous steps fail. However, they have an option to skip
	// execution if previous Pre and Test steps passed.
	Post []TestStep `json:"post,omitempty"`
	// Workflow is the name of the workflow to be used for this configuration. For fields defined in both
	// the config and the workflow, the fields from the config will override what is set in Workflow.
	Workflow *string `json:"workflow,omitempty"`
	// Environment has the values of parameters for the steps.
	Environment TestEnvironment `json:"env,omitempty"`
	// Dependencies holds override values for dependency parameters.
	Dependencies TestDependencies `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// AllowSkipOnSuccess defines if any steps can be skipped when
	// all previous `pre` and `test` steps were successful. The given step must explicitly
	// ask for being skipped by setting the OptionalOnSuccess flag to true.
	AllowSkipOnSuccess *bool `json:"allow_skip_on_success,omitempty"`
	// AllowBestEffortPostSteps defines if any `post` steps can be ignored when
	// they fail. The given step must explicitly ask for being ignored by setting
	// the OptionalOnSuccess flag to true.
	AllowBestEffortPostSteps *bool `json:"allow_best_effort_post_steps,omitempty"`
	// Observers are the observers that should be running
	Observers *Observers `json:"observers,omitempty"`
	// DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever
	// be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.
	DependencyOverrides DependencyOverrides `json:"dependency_overrides,omitempty"`
	// NodeArchitecture is the architecture for the node where the test will run.
	// If set, the generated test pod will include a nodeSelector for this architecture.
	NodeArchitecture *NodeArchitecture `json:"node_architecture,omitempty"`
	// NodeArchitectureOverrides is a map that allows overriding the node architecture for specific steps
	// that exist in the Pre, Test and Post steps. The key is the name of the step and the value is the architecture.
	NodeArchitectureOverrides NodeArchitectureOverrides `json:"node_architecture_overrides,omitempty"`
}

type NodeArchitectureOverrides map[string]NodeArchitecture
type DependencyOverrides map[string]string

// MultiStageTestConfigurationLiteral is a form of the MultiStageTestConfiguration that does not include
// references. It is the type that MultiStageTestConfigurations are converted to when parsed by the
// ci-operator-configresolver.
type MultiStageTestConfigurationLiteral struct {
	// ClusterProfile defines the profile/cloud provider for end-to-end test steps.
	ClusterProfile ClusterProfile `json:"cluster_profile"`
	// Pre is the array of test steps run to set up the environment for the test.
	Pre []LiteralTestStep `json:"pre,omitempty"`
	// Test is the array of test steps that define the actual test.
	Test []LiteralTestStep `json:"test,omitempty"`
	// Post is the array of test steps run after the tests finish and teardown/deprovision resources.
	// Post steps always run, even if previous steps fail.
	Post []LiteralTestStep `json:"post,omitempty"`
	// Environment has the values of parameters for the steps.
	Environment TestEnvironment `json:"env,omitempty"`
	// Dependencies holds override values for dependency parameters.
	Dependencies TestDependencies `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// AllowSkipOnSuccess defines if any steps can be skipped when
	// all previous `pre` and `test` steps were successful. The given step must explicitly
	// ask for being skipped by setting the OptionalOnSuccess flag to true.
	AllowSkipOnSuccess *bool `json:"allow_skip_on_success,omitempty"`
	// AllowBestEffortPostSteps defines if any `post` steps can be ignored when
	// they fail. The given step must explicitly ask for being ignored by setting
	// the OptionalOnSuccess flag to true.
	AllowBestEffortPostSteps *bool `json:"allow_best_effort_post_steps,omitempty"`
	// Observers are the observers that need to be run
	Observers []Observer `json:"observers,omitempty"`
	// DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever
	// be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.
	DependencyOverrides DependencyOverrides `json:"dependency_overrides,omitempty"`
	// NodeArchitecture is the architecture for the node where the test will run.
	// If set, the generated test pod will include a nodeSelector for this architecture.
	NodeArchitecture *NodeArchitecture `json:"node_architecture,omitempty"`
	// NodeArchitectureOverrides is a map that allows overriding the node architecture for specific steps
	// that exist in the Pre, Test and Post steps. The key is the name of the step and the value is the architecture.
	NodeArchitectureOverrides NodeArchitectureOverrides `json:"node_architecture_overrides,omitempty"`
	// Override job timeout
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
}

// TestEnvironment has the values of parameters for multi-stage tests.
type TestEnvironment map[string]string

// TestDependencies has the values of dependency overrides for multi-stage tests.
type TestDependencies map[string]string

// Secret describes a secret to be mounted inside a test
// container.
type Secret struct {
	// Secret name, used inside test containers
	Name string `json:"name"`
	// Secret mount path. Defaults to /usr/test-secrets for first
	// secret. /usr/test-secrets-2 for second, and so on.
	MountPath string `json:"mount_path"`
}

// MemoryBackedVolume describes a tmpfs (memory backed volume)
// that will be mounted into a test container at /tmp/volume.
// Use with tests that need extremely fast disk, such as those
// that run an etcd server or other IO-intensive workload.
type MemoryBackedVolume struct {
	// Size is the requested size of the volume as a Kubernetes
	// quantity, i.e. "1Gi" or "500M"
	Size string `json:"size"`
}

// ContainerTestConfiguration describes a test that runs a
// command in one of the previously built images.
type ContainerTestConfiguration struct {
	// From is the image stream tag in the pipeline to run this
	// command in.
	From PipelineImageStreamTagReference `json:"from"`
	// MemoryBackedVolume mounts a volume of the specified size into
	// the container at /tmp/volume.
	MemoryBackedVolume *MemoryBackedVolume `json:"memory_backed_volume,omitempty"`
	// If the step should clone the source code prior to running the command.
	// Defaults to `true` for `base_images`, `false` otherwise.
	Clone *bool `json:"clone,omitempty"`
}

// ClusterProfile is the name of a set of input variables
// provided to the installer defining the target cloud,
// cluster topology, etc.
type ClusterProfile string

const (
	ClusterProfileAWS                     ClusterProfile = "aws"
	ClusterProfileAWSCSPIQE               ClusterProfile = "aws-cspi-qe"
	ClusterProfileAWSQE                   ClusterProfile = "aws-qe"
	ClusterProfileAWSC2SQE                ClusterProfile = "aws-c2s-qe"
	ClusterProfileAWSChinaQE              ClusterProfile = "aws-china-qe"
	ClusterProfileAWSGovCloudQE           ClusterProfile = "aws-usgov-qe"
	ClusterProfileAWSSC2SQE               ClusterProfile = "aws-sc2s-qe"
	ClusterProfileAWSSCPQE                ClusterProfile = "aws-scp-qe"
	ClusterProfileAWS1QE                  ClusterProfile = "aws-1-qe"
	ClusterProfileAWSAutoreleaseQE        ClusterProfile = "aws-autorelease-qe"
	ClusterProfileAWSSdQE                 ClusterProfile = "aws-sd-qe"
	ClusterProfileOEXAWSQE                ClusterProfile = "oex-aws-qe"
	ClusterProfileHyperfleetE2E           ClusterProfile = "hyperfleet-e2e"
	ClusterProfileAWSPerfScale            ClusterProfile = "aws-perfscale"
	ClusterProfileAWSPerfScaleOKD         ClusterProfile = "aws-perfscale-okd"
	ClusterProfileAWSPerfScaleQE          ClusterProfile = "aws-perfscale-qe"
	ClusterProfileAWSPerfScaleLRCQE       ClusterProfile = "aws-perfscale-lrc-qe"
	ClusterProfileAWSRestrictedQE         ClusterProfile = "aws-restricted-qe"
	ClusterProfileROSAE2E01               ClusterProfile = "rosa-e2e-01"
	ClusterProfileROSAE2E02               ClusterProfile = "rosa-e2e-02"
	ClusterProfileROSAE2E03               ClusterProfile = "rosa-e2e-03"
	ClusterProfileAWSEUSC                 ClusterProfile = "aws-eusc"
	ClusterProfileAWSOutpostQE            ClusterProfile = "aws-outpost-qe"
	ClusterProfileAWSChaos                ClusterProfile = "aws-chaos"
	ClusterProfileAWSManagedCSPIQE        ClusterProfile = "aws-managed-cspi-qe"
	ClusterProfileAWSOSDMSP               ClusterProfile = "aws-osd-msp"
	ClusterProfileAWSINTEROPQE            ClusterProfile = "aws-interop-qe"
	ClusterProfileAWSTerraformQE          ClusterProfile = "aws-terraform-qe"
	ClusterProfileAWSPipelinesPerf        ClusterProfile = "aws-pipelines-performance"
	ClusterProfileAWSRHTAPQE              ClusterProfile = "aws-rhtap-qe"
	ClusterProfileAWSKonfluxQE            ClusterProfile = "aws-konflux-qe"
	ClusterProfileAWSRHTAPPerformance     ClusterProfile = "aws-rhtap-performance"
	ClusterProfileAWSRHDHPerf             ClusterProfile = "aws-rhdh-performance"
	ClusterProfileAWSServerless           ClusterProfile = "aws-serverless"
	ClusterProfileAWSTelco                ClusterProfile = "aws-telco"
	ClusterProfileAWSOpendatahub          ClusterProfile = "aws-opendatahub"
	ClusterProfileAWSDevfile              ClusterProfile = "aws-devfile"
	ClusterProfileAWSSPLAT                ClusterProfile = "aws-splat"
	ClusterProfileAWSSustAutoRel412       ClusterProfile = "aws-sustaining-autorelease-412"
	ClusterProfileAWSKubeVirt             ClusterProfile = "aws-kubevirt"
	ClusterProfileAWSOVNPerfScale         ClusterProfile = "aws-ovn-perfscale"
	ClusterProfileAWSConfidentialQE       ClusterProfile = "aws-confidential-qe"
	ClusterProfileAlibabaCloud            ClusterProfile = "alibabacloud"
	ClusterProfileAlibabaCloudQE          ClusterProfile = "alibabacloud-qe"
	ClusterProfileAlibabaCloudCNQE        ClusterProfile = "alibabacloud-cn-qe"
	ClusterProfileAzure                   ClusterProfile = "azure"
	ClusterProfileAzure2                  ClusterProfile = "azure-2"
	ClusterProfileAzure4                  ClusterProfile = "azure4"
	ClusterProfileAzureArc                ClusterProfile = "azure-arc"
	ClusterProfileAzureArm64              ClusterProfile = "azure-arm64"
	ClusterProfileAzurePerfScale          ClusterProfile = "azure-perfscale"
	ClusterProfileAzureStack              ClusterProfile = "azurestack"
	ClusterProfileAzureStackDEV           ClusterProfile = "azurestack-dev"
	ClusterProfileAzureStackQE            ClusterProfile = "azurestack-qe"
	ClusterProfileAzureMag                ClusterProfile = "azuremag"
	ClusterProfileAzureQE                 ClusterProfile = "azure-qe"
	ClusterProfileAzureObservability      ClusterProfile = "azure-observability"
	ClusterProfileAzureHCPQE              ClusterProfile = "azure-hcp-qe"
	ClusterProfileAzureHCPHAQE            ClusterProfile = "azure-hcp-ha-qe"
	ClusterProfileAzureAutoreleaseQE      ClusterProfile = "azure-autorelease-qe"
	ClusterProfileAzureArm64QE            ClusterProfile = "azure-arm64-qe"
	ClusterProfileAzureMagQE              ClusterProfile = "azuremag-qe"
	ClusterProfileAzureSustAutoRel412     ClusterProfile = "azure-sustaining-autorelease-412"
	ClusterProfileAzureConfidentialQE     ClusterProfile = "azure-confidential-qe"
	ClusterProfileAzureCNVDevOps          ClusterProfile = "azure-cnv-devops"
	ClusterProfileEquinixOcpMetal         ClusterProfile = "equinix-ocp-metal"
	ClusterProfileEquinixOcpMetalQE       ClusterProfile = "equinix-ocp-metal-qe"
	ClusterProfileEquinixOcpHCP           ClusterProfile = "equinix-ocp-hcp"
	ClusterProfileFleetManagerQE          ClusterProfile = "fleet-manager-qe"
	ClusterProfileGCPQE                   ClusterProfile = "gcp-qe"
	ClusterProfileGCPQEC3Metal            ClusterProfile = "gcp-qe-c3-metal"
	ClusterProfileGCPAutoReleaseQE        ClusterProfile = "gcp-autorelease-qe"
	ClusterProfileGCPArm64                ClusterProfile = "gcp-arm64"
	ClusterProfileGCP                     ClusterProfile = "gcp"
	ClusterProfileGCP3                    ClusterProfile = "gcp-3"
	ClusterProfileGCP2                    ClusterProfile = "gcp-openshift-gce-devel-ci-2"
	ClusterProfileGCPOpendatahub          ClusterProfile = "gcp-opendatahub"
	ClusterProfileGCPTelco                ClusterProfile = "gcp-telco"
	ClusterProfileGCPChaos                ClusterProfile = "gcp-chaos"
	ClusterProfileGCPConfidentialQE       ClusterProfile = "gcp-confidential-qe"
	ClusterProfileGCPPerfScaleQE          ClusterProfile = "gcp-perfscale-qe"
	ClusterProfileIBMCloud                ClusterProfile = "ibmcloud"
	ClusterProfileGCPSustAutoRel412       ClusterProfile = "gcp-sustaining-autorelease-412"
	ClusterProfileIBMCloudCSPIQE          ClusterProfile = "ibmcloud-cspi-qe"
	ClusterProfileIBMCloudRHOAIQE         ClusterProfile = "ibmcloud-rhoai-qe"
	ClusterProfileIBMCloudQE              ClusterProfile = "ibmcloud-qe"
	ClusterProfileIBMCloudQE2             ClusterProfile = "ibmcloud-qe-2"
	ClusterProfileIBMCloudMultiPpc64le    ClusterProfile = "ibmcloud-multi-ppc64le"
	ClusterProfileIBMCloudMultiS390x      ClusterProfile = "ibmcloud-multi-s390x"
	ClusterProfilePOWERVC1                ClusterProfile = "powervc-1"
	ClusterProfilePOWERVSMulti1           ClusterProfile = "powervs-multi-1"
	ClusterProfilePOWERVS1                ClusterProfile = "powervs-1"
	ClusterProfilePOWERVS2                ClusterProfile = "powervs-2"
	ClusterProfilePOWERVS3                ClusterProfile = "powervs-3"
	ClusterProfilePOWERVS4                ClusterProfile = "powervs-4"
	ClusterProfilePOWERVS5                ClusterProfile = "powervs-5"
	ClusterProfilePOWERVS6                ClusterProfile = "powervs-6"
	ClusterProfilePOWERVS7                ClusterProfile = "powervs-7"
	ClusterProfilePOWERVS8                ClusterProfile = "powervs-8"
	ClusterProfilePOWERVS9                ClusterProfile = "powervs-9"
	ClusterProfileLibvirtPpc64le          ClusterProfile = "libvirt-ppc64le"
	ClusterProfileLibvirtPpc64leS2S       ClusterProfile = "libvirt-ppc64le-s2s"
	ClusterProfileLibvirtS390x            ClusterProfile = "libvirt-s390x"
	ClusterProfileLibvirtS390x1           ClusterProfile = "libvirt-s390x-1"
	ClusterProfileLibvirtS390x2           ClusterProfile = "libvirt-s390x-2"
	ClusterProfileLibvirtS390xAmd64       ClusterProfile = "libvirt-s390x-amd64"
	ClusterProfileLibvirtS390xVPN         ClusterProfile = "libvirt-s390x-vpn"
	ClusterProfileMetalPerfscaleBMCPT     ClusterProfile = "metal-perfscale-cpt"
	ClusterProfileMetalPerfscaleJetlag    ClusterProfile = "metal-perfscale-jetlag"
	ClusterProfileMetalPerfscaleOSP       ClusterProfile = "metal-perfscale-osp"
	ClusterProfileMetalPerfscaleSelfSched ClusterProfile = "metal-perfscale-selfsched"
	ClusterProfileMetalPerfscaleTelco     ClusterProfile = "metal-perfscale-telco"
	ClusterProfileMetalTelco5G            ClusterProfile = "metal-telco5g"
	ClusterProfileMetalTelcoV10N          ClusterProfile = "metal-telcov10n"
	ClusterProfileMetalTelco5GPTP         ClusterProfile = "metal-telco5g-ptp"
	ClusterProfileNutanix                 ClusterProfile = "nutanix"
	ClusterProfileNutanixQE               ClusterProfile = "nutanix-qe"
	ClusterProfileNutanixQEDis            ClusterProfile = "nutanix-qe-dis"
	ClusterProfileNutanixQEZone           ClusterProfile = "nutanix-qe-zone"
	ClusterProfileNutanixQEGPU            ClusterProfile = "nutanix-qe-gpu"
	ClusterProfileNutanixQEFlow           ClusterProfile = "nutanix-qe-flow"
	ClusterProfileOpenStackHwoffload      ClusterProfile = "openstack-hwoffload"
	ClusterProfileOpenStackIBMOSP         ClusterProfile = "openstack-ibm-osp"
	ClusterProfileOpenStackNFV            ClusterProfile = "openstack-nfv"
	ClusterProfileOpenStackMechaCentral   ClusterProfile = "openstack-vh-mecha-central"
	ClusterProfileOpenStackMechaAz0       ClusterProfile = "openstack-vh-mecha-az0"
	ClusterProfileOpenStackOsuosl         ClusterProfile = "openstack-osuosl"
	ClusterProfileOpenStackVexxhost       ClusterProfile = "openstack-vexxhost"
	ClusterProfileOpenStackVexxhostRHOS   ClusterProfile = "openstack-vh-bm-rhos"
	ClusterProfileOpenStackPpc64le        ClusterProfile = "openstack-ppc64le"
	ClusterProfileOpenStackOpVexxhost     ClusterProfile = "openstack-operators-vexxhost"
	ClusterProfileOpenStackNercDev        ClusterProfile = "openstack-nerc-dev"
	ClusterProfileOpenStackRHOSO          ClusterProfile = "openstack-rhoso"
	ClusterProfileOpenStackRHOSCI         ClusterProfile = "openstack-rhos-ci"
	ClusterProfileOvirt                   ClusterProfile = "ovirt"
	ClusterProfilePacket                  ClusterProfile = "packet"
	ClusterProfilePacketAssisted          ClusterProfile = "packet-assisted"
	ClusterProfilePacketSNO               ClusterProfile = "packet-sno"
	ClusterProfileVSphereDis2             ClusterProfile = "vsphere-dis-2"
	ClusterProfileVSphereMultizone2       ClusterProfile = "vsphere-multizone-2"
	ClusterProfileVSphereConnected2       ClusterProfile = "vsphere-connected-2"
	ClusterProfileVSphereElastic          ClusterProfile = "vsphere-elastic"
	ClusterProfileVSphereElasticPoc       ClusterProfile = "vsphere-elastic-poc"
	ClusterProfileKubevirt                ClusterProfile = "kubevirt"
	ClusterProfileAWSCPaaS                ClusterProfile = "aws-cpaas"
	ClusterProfileOSDEphemeral            ClusterProfile = "osd-ephemeral"
	ClusterProfileAWS2                    ClusterProfile = "aws-2"
	ClusterProfileAWS3                    ClusterProfile = "aws-3"
	ClusterProfileAWS4                    ClusterProfile = "aws-4"
	ClusterProfileAWS5                    ClusterProfile = "aws-5"
	ClusterProfileOpenshiftOrgAWS         ClusterProfile = "openshift-org-aws"
	ClusterProfileOpenshiftOrgAzure       ClusterProfile = "openshift-org-azure"
	ClusterProfileGCPVirtualization       ClusterProfile = "gcp-virtualization"
	ClusterProfileAWSVirtualization       ClusterProfile = "aws-virtualization"
	ClusterProfileAzureVirtualization     ClusterProfile = "azure-virtualization"
	ClusterProfileOCIAgent                ClusterProfile = "oci-agent-qe"
	ClusterProfileOCIAssisted             ClusterProfile = "oci-assisted"
	ClusterProfileHypershiftAWS           ClusterProfile = "hypershift-aws"
	ClusterProfileHypershiftAzure         ClusterProfile = "hypershift-azure"
	ClusterProfileHypershiftAKS           ClusterProfile = "hypershift-aks"
	ClusterProfileHypershiftPowerVS       ClusterProfile = "hypershift-powervs"
	ClusterProfileHypershiftPowerVSCB     ClusterProfile = "hypershift-powervs-cb"
	ClusterProfileHypershiftGCP           ClusterProfile = "hypershift-gcp"
	ClusterProfileOSSM                    ClusterProfile = "ossm-aws"
	ClusterProfileMedik8sAWS              ClusterProfile = "medik8s-aws"
	ClusterProfileGitOpsAWS               ClusterProfile = "gitops-aws"
	ClusterProfileCheAWS                  ClusterProfile = "che-aws"
	ClusterProfileOSLGCP                  ClusterProfile = "osl-gcp"
	ClusterProfileDevSandboxCIAWS         ClusterProfile = "devsandboxci-aws"
	ClusterProfileQuayAWS                 ClusterProfile = "quay-aws"
	ClusterProfileAWSEdgeInfra            ClusterProfile = "aws-edge-infra"
	ClusterProfileRHOpenShiftEcosystem    ClusterProfile = "rh-openshift-ecosystem"
	ClusterProfileODFAWS                  ClusterProfile = "odf-aws"
	ClusterProfileKonfluxWorkspacesAWS    ClusterProfile = "konfluxworkspaces-aws"
	ClusterProfileAWSObservabiltity       ClusterProfile = "aws-observability"
	ClusterProfileAWSStackrox             ClusterProfile = "aws-stackrox"
	ClusterProfileAWSSDCICD               ClusterProfile = "aws-sd-cicd"
	ClusterProfileGCPSDCICD               ClusterProfile = "gcp-sd-cicd"
	ClusterProfileAroRH                   ClusterProfile = "aro-redhat-tenant"
	ClusterProfileAWSRHOAIQE              ClusterProfile = "aws-rhoai-qe"
	ClusterProfileAWSManagedRosaRHOAIQE   ClusterProfile = "aws-managed-rosa-rhoai-qe"
	ClusterProfileAWSManagedOSDRHOAIQE    ClusterProfile = "aws-managed-osd-rhoai-qe"
	ClusterProfileGCPObservability        ClusterProfile = "gcp-observability"
	ClusterProfileEquinixEdgeEnablement   ClusterProfile = "equinix-edge-enablement"
	ClusterProfileAWSQUAYQE               ClusterProfile = "aws-quay-qe"
	ClusterProfileGCPQUAYQE               ClusterProfile = "gcp-quay-qe"
	ClusterProfileAzureQUAYQE             ClusterProfile = "azure-quay-qe"
	ClusterProfileAWSMCOQE                ClusterProfile = "aws-mco-qe"
	ClusterProfileAWSOADPQE               ClusterProfile = "aws-oadp-qe"
	ClusterProfileAzureOADPQE             ClusterProfile = "azure-oadp-qe"
	ClusterProfileGCPOADPQE               ClusterProfile = "gcp-oadp-qe"
	ClusterProfileAWSlpChaos              ClusterProfile = "aws-lp-chaos"
	ClusterProfileMetalRHgs               ClusterProfile = "metal-redhat-gs"
	ClusterProfileAWSOSPQE                ClusterProfile = "aws-osp-qe"
	ClusterProfileAWSOSC                  ClusterProfile = "aws-sandboxed-containers-operator"

	ClusterProfileRosaRegionalPlatformInt ClusterProfile = "rosa-regional-platform-int"

	ClusterProfileAROHCPInt  ClusterProfile = "aro-hcp-int"
	ClusterProfileAROHCPStg  ClusterProfile = "aro-hcp-stg"
	ClusterProfileAROHCPProd ClusterProfile = "aro-hcp-prod"
	ClusterProfileAROHCPDev  ClusterProfile = "aro-hcp-dev"

	ClusterProfileAROClassicInt  ClusterProfile = "aro-classic-int"
	ClusterProfileAROClassicStg  ClusterProfile = "aro-classic-stg"
	ClusterProfileAROClassicProd ClusterProfile = "aro-classic-prod"
	ClusterProfileAROClassicDev  ClusterProfile = "aro-classic-dev"
)

// ClusterProfiles are all valid cluster profiles
func ClusterProfiles() []ClusterProfile {
	return []ClusterProfile{
		ClusterProfileAWS,
		ClusterProfileAWS2,
		ClusterProfileAWS3,
		ClusterProfileAWS4,
		ClusterProfileAWS5,
		ClusterProfileOpenshiftOrgAWS,
		ClusterProfileOpenshiftOrgAzure,
		ClusterProfileAWSC2SQE,
		ClusterProfileAWSCPaaS,
		ClusterProfileAWSCSPIQE,
		ClusterProfileAWSPerfScale,
		ClusterProfileAWSPerfScaleOKD,
		ClusterProfileAWSPerfScaleQE,
		ClusterProfileAWSPerfScaleLRCQE,
		ClusterProfileAWSRestrictedQE,
		ClusterProfileROSAE2E01,
		ClusterProfileROSAE2E02,
		ClusterProfileROSAE2E03,
		ClusterProfileAWSEUSC,
		ClusterProfileAWSChaos,
		ClusterProfileAWSChinaQE,
		ClusterProfileAWSManagedCSPIQE,
		ClusterProfileAWSGovCloudQE,
		ClusterProfileAWSOSDMSP,
		ClusterProfileAWSQE,
		ClusterProfileAWS1QE,
		ClusterProfileAWSAutoreleaseQE,
		ClusterProfileAWSSdQE,
		ClusterProfileOEXAWSQE,
		ClusterProfileHyperfleetE2E,
		ClusterProfileAWSSC2SQE,
		ClusterProfileAWSSCPQE,
		ClusterProfileAWSOutpostQE,
		ClusterProfileAWSINTEROPQE,
		ClusterProfileAWSTerraformQE,
		ClusterProfileAWSPipelinesPerf,
		ClusterProfileAWSRHTAPQE,
		ClusterProfileAWSKonfluxQE,
		ClusterProfileAWSRHTAPPerformance,
		ClusterProfileAWSRHDHPerf,
		ClusterProfileAWSServerless,
		ClusterProfileAWSStackrox,
		ClusterProfileAWSTelco,
		ClusterProfileAWSOpendatahub,
		ClusterProfileAWSDevfile,
		ClusterProfileAWSSPLAT,
		ClusterProfileAWSSustAutoRel412,
		ClusterProfileAWSKubeVirt,
		ClusterProfileAWSOVNPerfScale,
		ClusterProfileAWSConfidentialQE,
		ClusterProfileAlibabaCloud,
		ClusterProfileAlibabaCloudQE,
		ClusterProfileAlibabaCloudCNQE,
		ClusterProfileAzure2,
		ClusterProfileAzure4,
		ClusterProfileAzureArc,
		ClusterProfileAzureArm64,
		ClusterProfileAzureArm64QE,
		ClusterProfileAzureMag,
		ClusterProfileAzureMagQE,
		ClusterProfileAzureQE,
		ClusterProfileAzureObservability,
		ClusterProfileAzureHCPQE,
		ClusterProfileAzureHCPHAQE,
		ClusterProfileAzureAutoreleaseQE,
		ClusterProfileAzurePerfScale,
		ClusterProfileAzureStack,
		ClusterProfileAzureStackDEV,
		ClusterProfileAzureStackQE,
		ClusterProfileAzureSustAutoRel412,
		ClusterProfileAzureConfidentialQE,
		ClusterProfileAzureCNVDevOps,
		ClusterProfileEquinixOcpMetal,
		ClusterProfileEquinixOcpMetalQE,
		ClusterProfileEquinixOcpHCP,
		ClusterProfileFleetManagerQE,
		ClusterProfileGCP,
		ClusterProfileGCP2,
		ClusterProfileGCP3,
		ClusterProfileGCPQE,
		ClusterProfileGCPQEC3Metal,
		ClusterProfileGCPAutoReleaseQE,
		ClusterProfileGCPArm64,
		ClusterProfileGCPVirtualization,
		ClusterProfileGCPOpendatahub,
		ClusterProfileGCPTelco,
		ClusterProfileGCPChaos,
		ClusterProfileGCPConfidentialQE,
		ClusterProfileGCPPerfScaleQE,
		ClusterProfileGCPSustAutoRel412,
		ClusterProfileGCPObservability,
		ClusterProfileAWSVirtualization,
		ClusterProfileAzureVirtualization,
		ClusterProfileIBMCloud,
		ClusterProfileIBMCloudCSPIQE,
		ClusterProfileIBMCloudRHOAIQE,
		ClusterProfileIBMCloudQE,
		ClusterProfileIBMCloudQE2,
		ClusterProfileIBMCloudMultiPpc64le,
		ClusterProfilePOWERVC1,
		ClusterProfilePOWERVSMulti1,
		ClusterProfileIBMCloudMultiS390x,
		ClusterProfilePOWERVS1,
		ClusterProfilePOWERVS2,
		ClusterProfilePOWERVS3,
		ClusterProfilePOWERVS4,
		ClusterProfilePOWERVS5,
		ClusterProfilePOWERVS6,
		ClusterProfilePOWERVS7,
		ClusterProfilePOWERVS8,
		ClusterProfilePOWERVS9,
		ClusterProfileKubevirt,
		ClusterProfileLibvirtPpc64le,
		ClusterProfileLibvirtPpc64leS2S,
		ClusterProfileLibvirtS390x,
		ClusterProfileLibvirtS390x1,
		ClusterProfileLibvirtS390x2,
		ClusterProfileLibvirtS390xAmd64,
		ClusterProfileLibvirtS390xVPN,
		ClusterProfileMetalPerfscaleBMCPT,
		ClusterProfileMetalPerfscaleJetlag,
		ClusterProfileMetalPerfscaleOSP,
		ClusterProfileMetalPerfscaleSelfSched,
		ClusterProfileMetalPerfscaleTelco,
		ClusterProfileMetalTelco5G,
		ClusterProfileMetalTelcoV10N,
		ClusterProfileMetalTelco5GPTP,
		ClusterProfileNutanix,
		ClusterProfileNutanixQE,
		ClusterProfileNutanixQEDis,
		ClusterProfileNutanixQEZone,
		ClusterProfileNutanixQEGPU,
		ClusterProfileNutanixQEFlow,
		ClusterProfileOSDEphemeral,
		ClusterProfileOpenStackHwoffload,
		ClusterProfileOpenStackIBMOSP,
		ClusterProfileOpenStackMechaAz0,
		ClusterProfileOpenStackMechaCentral,
		ClusterProfileOpenStackNFV,
		ClusterProfileOpenStackOsuosl,
		ClusterProfileOpenStackPpc64le,
		ClusterProfileOpenStackVexxhost,
		ClusterProfileOpenStackVexxhostRHOS,
		ClusterProfileOpenStackOpVexxhost,
		ClusterProfileOpenStackNercDev,
		ClusterProfileOpenStackRHOSO,
		ClusterProfileOpenStackRHOSCI,
		ClusterProfileOvirt,
		ClusterProfilePacket,
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO,

		ClusterProfileVSphereDis2,
		ClusterProfileVSphereMultizone2,
		ClusterProfileVSphereConnected2,
		ClusterProfileVSphereElastic,
		ClusterProfileVSphereElasticPoc,
		ClusterProfileOCIAgent,
		ClusterProfileOCIAssisted,
		ClusterProfileHypershiftAWS,
		ClusterProfileHypershiftAzure,
		ClusterProfileHypershiftAKS,
		ClusterProfileHypershiftPowerVS,
		ClusterProfileHypershiftPowerVSCB,
		ClusterProfileHypershiftGCP,
		ClusterProfileOSSM,
		ClusterProfileMedik8sAWS,
		ClusterProfileGitOpsAWS,
		ClusterProfileCheAWS,
		ClusterProfileOSLGCP,
		ClusterProfileDevSandboxCIAWS,
		ClusterProfileQuayAWS,
		ClusterProfileAWSEdgeInfra,
		ClusterProfileRHOpenShiftEcosystem,
		ClusterProfileODFAWS,
		ClusterProfileKonfluxWorkspacesAWS,
		ClusterProfileAWSObservabiltity,
		ClusterProfileAWSSDCICD,
		ClusterProfileGCPSDCICD,
		ClusterProfileAroRH,
		ClusterProfileAWSRHOAIQE,
		ClusterProfileAWSManagedRosaRHOAIQE,
		ClusterProfileAWSManagedOSDRHOAIQE,
		ClusterProfileEquinixEdgeEnablement,
		ClusterProfileAWSQUAYQE,
		ClusterProfileGCPQUAYQE,
		ClusterProfileAzureQUAYQE,
		ClusterProfileAWSMCOQE,
		ClusterProfileAWSOADPQE,
		ClusterProfileAzureOADPQE,
		ClusterProfileGCPOADPQE,
		ClusterProfileAWSlpChaos,
		ClusterProfileMetalRHgs,
		ClusterProfileAWSOSPQE,
		ClusterProfileAWSOSC,

		ClusterProfileRosaRegionalPlatformInt,

		ClusterProfileAROHCPInt,
		ClusterProfileAROHCPStg,
		ClusterProfileAROHCPProd,
		ClusterProfileAROHCPDev,

		ClusterProfileAROClassicInt,
		ClusterProfileAROClassicStg,
		ClusterProfileAROClassicProd,
		ClusterProfileAROClassicDev,
	}
}

func (p ClusterProfile) Name() string {
	return string(p)
}

// ClusterType maps profiles to the type string used by tests.
func (p ClusterProfile) ClusterType() string {
	switch p {
	case
		ClusterProfileAWS,
		ClusterProfileAWSCSPIQE,
		ClusterProfileAWSManagedCSPIQE,
		ClusterProfileAWSCPaaS,
		ClusterProfileAWS2,
		ClusterProfileAWS3,
		ClusterProfileAWS4,
		ClusterProfileAWS5,
		ClusterProfileOpenshiftOrgAWS,
		ClusterProfileAWSQE,
		ClusterProfileAWSINTEROPQE,
		ClusterProfileAWS1QE,
		ClusterProfileAWSAutoreleaseQE,
		ClusterProfileAWSSdQE,
		ClusterProfileOEXAWSQE,
		ClusterProfileAWSVirtualization,
		ClusterProfileFleetManagerQE,
		ClusterProfileAWSPerfScale,
		ClusterProfileAWSPerfScaleOKD,
		ClusterProfileAWSPerfScaleQE,
		ClusterProfileAWSPerfScaleLRCQE,
		ClusterProfileAWSRestrictedQE,
		ClusterProfileROSAE2E01,
		ClusterProfileROSAE2E02,
		ClusterProfileROSAE2E03,
		ClusterProfileAWSServerless,
		ClusterProfileAWSStackrox,
		ClusterProfileAWSOutpostQE,
		ClusterProfileAWSChaos,
		ClusterProfileAWSTerraformQE,
		ClusterProfileAWSPipelinesPerf,
		ClusterProfileAWSRHTAPQE,
		ClusterProfileAWSKonfluxQE,
		ClusterProfileAWSRHTAPPerformance,
		ClusterProfileAWSRHDHPerf,
		ClusterProfileAWSSPLAT,
		ClusterProfileAWSSustAutoRel412,
		ClusterProfileAWSKubeVirt,
		ClusterProfileAWSOVNPerfScale,
		ClusterProfileOSSM,
		ClusterProfileAWSOpendatahub,
		ClusterProfileAWSDevfile,
		ClusterProfileAWSTelco,
		ClusterProfileMedik8sAWS,
		ClusterProfileGitOpsAWS,
		ClusterProfileCheAWS,
		ClusterProfileDevSandboxCIAWS,
		ClusterProfileQuayAWS,
		ClusterProfileAWSEdgeInfra,
		ClusterProfileODFAWS,
		ClusterProfileAWSObservabiltity,
		ClusterProfileAWSSDCICD,
		ClusterProfileKonfluxWorkspacesAWS,
		ClusterProfileAWSRHOAIQE,
		ClusterProfileAWSManagedRosaRHOAIQE,
		ClusterProfileAWSQUAYQE,
		ClusterProfileAWSMCOQE,
		ClusterProfileAWSManagedOSDRHOAIQE,
		ClusterProfileAWSOADPQE,
		ClusterProfileAWSConfidentialQE,
		ClusterProfileAWSlpChaos,
		ClusterProfileAWSOSPQE,
		ClusterProfileAWSOSC:
		return string(CloudAWS)
	case
		ClusterProfileAlibabaCloud,
		ClusterProfileAlibabaCloudQE,
		ClusterProfileAlibabaCloudCNQE:
		return "alibabacloud"
	case ClusterProfileAWSC2SQE:
		return "aws-c2s"
	case ClusterProfileAWSChinaQE:
		return "aws-china"
	case ClusterProfileAWSGovCloudQE:
		return "aws-usgov"
	case ClusterProfileAWSSC2SQE:
		return "aws-sc2s"
	case ClusterProfileAWSEUSC:
		return "aws-eusc"
	case ClusterProfileAWSSCPQE:
		return "aws-scp"
	case ClusterProfileAWSOSDMSP:
		return "aws-osd-msp"
	case
		ClusterProfileAzure2,
		ClusterProfileAzure4,
		ClusterProfileOpenshiftOrgAzure,
		ClusterProfileAzureArc,
		ClusterProfileAzureQE,
		ClusterProfileAzureObservability,
		ClusterProfileAzureHCPQE,
		ClusterProfileAzureHCPHAQE,
		ClusterProfileAzureAutoreleaseQE,
		ClusterProfileAzurePerfScale,
		ClusterProfileAzureSustAutoRel412,
		ClusterProfileAzureQUAYQE,
		ClusterProfileAzureConfidentialQE,
		ClusterProfileAzureCNVDevOps,
		ClusterProfileAzureVirtualization,
		ClusterProfileAzureOADPQE:
		return "azure4"
	case
		ClusterProfileAzureArm64,
		ClusterProfileAzureArm64QE:
		return "azure-arm64"
	case
		ClusterProfileAzureStack,
		ClusterProfileAzureStackDEV,
		ClusterProfileAzureStackQE:
		return "azurestack"
	case
		ClusterProfileAzureMag,
		ClusterProfileAzureMagQE:
		return "azuremag"
	case
		ClusterProfileEquinixOcpMetal,
		ClusterProfileEquinixOcpMetalQE,
		ClusterProfileEquinixOcpHCP:
		return "equinix-ocp-metal"
	case
		ClusterProfileGCPQE,
		ClusterProfileGCPQEC3Metal,
		ClusterProfileGCPAutoReleaseQE,
		ClusterProfileGCPArm64,
		ClusterProfileGCP,
		ClusterProfileGCP3,
		ClusterProfileGCP2,
		ClusterProfileGCPVirtualization,
		ClusterProfileGCPSustAutoRel412,
		ClusterProfileGCPObservability,
		ClusterProfileGCPOpendatahub,
		ClusterProfileGCPTelco,
		ClusterProfileGCPChaos,
		ClusterProfileGCPConfidentialQE,
		ClusterProfileGCPPerfScaleQE,
		ClusterProfileGCPSDCICD,
		ClusterProfileGCPQUAYQE,
		ClusterProfileOSLGCP:
		return string(CloudGCP)
	case
		ClusterProfileIBMCloud,
		ClusterProfileIBMCloudCSPIQE,
		ClusterProfileIBMCloudQE,
		ClusterProfileIBMCloudQE2,
		ClusterProfileIBMCloudRHOAIQE:
		return "ibmcloud"
	case ClusterProfileIBMCloudMultiPpc64le:
		return "ibmcloud-multi-ppc64le"
	case ClusterProfileIBMCloudMultiS390x:
		return "ibmcloud-multi-s390x"
	case ClusterProfilePOWERVC1:
		return "powervc-1"
	case ClusterProfilePOWERVSMulti1:
		return "powervs-multi-1"
	case ClusterProfilePOWERVS1:
		return "powervs-1"
	case ClusterProfilePOWERVS2:
		return "powervs-2"
	case ClusterProfilePOWERVS3:
		return "powervs-3"
	case ClusterProfilePOWERVS4:
		return "powervs-4"
	case ClusterProfilePOWERVS5:
		return "powervs-5"
	case ClusterProfilePOWERVS6:
		return "powervs-6"
	case ClusterProfilePOWERVS7:
		return "powervs-7"
	case ClusterProfilePOWERVS8:
		return "powervs-8"
	case ClusterProfilePOWERVS9:
		return "powervs-9"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le"
	case ClusterProfileLibvirtPpc64leS2S:
		return "libvirt-ppc64le-s2s"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x"
	case ClusterProfileLibvirtS390x1:
		return "libvirt-s390x-1"
	case ClusterProfileLibvirtS390x2:
		return "libvirt-s390x-2"
	case ClusterProfileLibvirtS390xAmd64:
		return "libvirt-s390x-amd64"
	case ClusterProfileLibvirtS390xVPN:
		return "libvirt-s390x-vpn"
	case ClusterProfileMetalRHgs:
		return "metal-redhat-gs"
	case ClusterProfileMetalPerfscaleBMCPT:
		return "metal-perfscale-cpt"
	case ClusterProfileMetalPerfscaleJetlag:
		return "metal-perfscale-jetlag"
	case ClusterProfileMetalPerfscaleOSP:
		return "metal-perfscale-osp"
	case ClusterProfileMetalPerfscaleSelfSched:
		return "metal-perfscale-selfsched"
	case ClusterProfileMetalPerfscaleTelco:
		return "metal-perfscale-telco"
	case ClusterProfileMetalTelco5G:
		return "metal-telco5g"
	case ClusterProfileMetalTelco5GPTP:
		return "metal-telco5g-ptp"
	case ClusterProfileMetalTelcoV10N:
		return "metal-telcov10n"
	case
		ClusterProfileNutanix,
		ClusterProfileNutanixQE,
		ClusterProfileNutanixQEDis,
		ClusterProfileNutanixQEZone,
		ClusterProfileNutanixQEGPU,
		ClusterProfileNutanixQEFlow:
		return "nutanix"
	case ClusterProfileOpenStackHwoffload:
		return "openstack-hwoffload"
	case ClusterProfileOpenStackIBMOSP:
		return "openstack-ibm-osp"
	case ClusterProfileOpenStackNFV:
		return "openstack-nfv"
	case ClusterProfileOpenStackMechaCentral:
		return "openstack-vh-mecha-central"
	case ClusterProfileOpenStackMechaAz0:
		return "openstack-vh-mecha-az0"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost"
	case ClusterProfileOpenStackVexxhostRHOS:
		return "openstack-vh-bm-rhos"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le"
	case ClusterProfileOpenStackOpVexxhost:
		return "openstack-operators-vexxhost"
	case ClusterProfileOpenStackNercDev:
		return "openstack-nerc-dev"
	case ClusterProfileOpenStackRHOSO:
		return "openstack-rhoso"
	case ClusterProfileOpenStackRHOSCI:
		return "openstack-rhos-ci"
	case
		ClusterProfileVSphereMultizone2,
		ClusterProfileVSphereDis2,
		ClusterProfileVSphereElastic,
		ClusterProfileVSphereConnected2,
		ClusterProfileVSphereElasticPoc:

		return "vsphere"
	case ClusterProfileOvirt:
		return "ovirt"
	case
		ClusterProfilePacket:
		return "packet"
	case
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO:
		return "packet-edge"
	case ClusterProfileKubevirt:
		return "kubevirt"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral"
	case ClusterProfileHyperfleetE2E:
		return "hyperfleet-e2e"
	case ClusterProfileOCIAgent:
		return "oci-agent-qe"
	case ClusterProfileOCIAssisted:
		return "oci-edge"
	case ClusterProfileHypershiftAWS:
		return "hypershift-aws"
	case ClusterProfileHypershiftAzure:
		return "hypershift-azure"
	case ClusterProfileHypershiftAKS:
		return "hypershift-aks"
	case ClusterProfileHypershiftPowerVS:
		return "hypershift-powervs"
	case ClusterProfileHypershiftPowerVSCB:
		return "hypershift-powervs-cb"
	case ClusterProfileHypershiftGCP:
		return "hypershift-gcp"
	case ClusterProfileRHOpenShiftEcosystem:
		return string(CloudAWS)
	case ClusterProfileAroRH:
		return "aro"
	case ClusterProfileEquinixEdgeEnablement:
		return "equinix-edge-enablement"

	case ClusterProfileRosaRegionalPlatformInt:
		return "rosa-regional-platform-int"

	case ClusterProfileAROHCPInt:
		return "aro-hcp-int"
	case ClusterProfileAROHCPStg:
		return "aro-hcp-stg"
	case ClusterProfileAROHCPProd:
		return "aro-hcp-prod"
	case ClusterProfileAROHCPDev:
		return "aro-hcp-dev"

	case ClusterProfileAROClassicInt:
		return "aro-classic-int"
	case ClusterProfileAROClassicStg:
		return "aro-classic-stg"
	case ClusterProfileAROClassicProd:
		return "aro-classic-prod"
	case ClusterProfileAROClassicDev:
		return "aro-classic-dev"

	default:
		return ""
	}
}

// LeaseType maps profiles to the type string used in leases.
func (p ClusterProfile) LeaseType() string {
	switch p {
	case
		ClusterProfileAWS:
		return "aws-quota-slice"
	case ClusterProfileAWSQE:
		return "aws-qe-quota-slice"
	case ClusterProfileAWS1QE:
		return "aws-1-qe-quota-slice"
	case ClusterProfileAWSAutoreleaseQE:
		return "aws-autorelease-qe-quota-slice"
	case ClusterProfileAWSSdQE:
		return "aws-sd-qe-quota-slice"
	case ClusterProfileOEXAWSQE:
		return "oex-aws-qe-quota-slice"
	case ClusterProfileHyperfleetE2E:
		return "hyperfleet-e2e-quota-slice"
	case ClusterProfileAWSOutpostQE:
		return "aws-outpost-qe-quota-slice"
	case ClusterProfileAWSC2SQE:
		return "aws-c2s-qe-quota-slice"
	case ClusterProfileAWSChinaQE:
		return "aws-china-qe-quota-slice"
	case ClusterProfileAWSCSPIQE:
		return "aws-cspi-qe-quota-slice"
	case ClusterProfileAWSChaos:
		return "aws-chaos-quota-slice"
	case ClusterProfileAWSPerfScale:
		return "aws-perfscale-quota-slice"
	case ClusterProfileAWSPerfScaleOKD:
		return "aws-perfscale-okd-quota-slice"
	case ClusterProfileAWSPerfScaleQE:
		return "aws-perfscale-qe-quota-slice"
	case ClusterProfileAWSPerfScaleLRCQE:
		return "aws-perfscale-lrc-qe-quota-slice"
	case ClusterProfileAWSRestrictedQE:
		return "aws-restricted-qe"
	case ClusterProfileROSAE2E01:
		return "rosa-e2e-01-quota-slice"
	case ClusterProfileROSAE2E02:
		return "rosa-e2e-02-quota-slice"
	case ClusterProfileROSAE2E03:
		return "rosa-e2e-03-quota-slice"
	case ClusterProfileAWSEUSC:
		return "aws-eusc-quota-slice"
	case ClusterProfileAWSManagedCSPIQE:
		return "aws-managed-cspi-qe-quota-slice"
	case ClusterProfileAWSGovCloudQE:
		return "aws-usgov-qe-quota-slice"
	case ClusterProfileAWSSC2SQE:
		return "aws-sc2s-qe-quota-slice"
	case ClusterProfileAWSSCPQE:
		return "aws-scp-qe-quota-slice"
	case ClusterProfileAWSSustAutoRel412:
		return "aws-sustaining-autorelease-412-quota-slice"
	case ClusterProfileAWSINTEROPQE:
		return "aws-interop-qe-quota-slice"
	case ClusterProfileAWSVirtualization:
		return "aws-virtualization-quota-slice"
	case ClusterProfileAWSTerraformQE:
		return "aws-terraform-qe-quota-slice"
	case ClusterProfileAWSPipelinesPerf:
		return "aws-pipelines-performance-quota-slice"
	case ClusterProfileAWSRHTAPQE:
		return "aws-rhtap-qe-quota-slice"
	case ClusterProfileAWSKonfluxQE:
		return "aws-konflux-qe-quota-slice"
	case ClusterProfileAWSRHTAPPerformance:
		return "aws-rhtap-performance-quota-slice"
	case ClusterProfileAWSRHDHPerf:
		return "aws-rhdh-performance-quota-slice"
	case ClusterProfileAWSServerless:
		return "aws-serverless-quota-slice"
	case ClusterProfileAWSStackrox:
		return "aws-stackrox-quota-slice"
	case ClusterProfileAWSTelco:
		return "aws-telco-quota-slice"
	case ClusterProfileAWSOpendatahub:
		return "aws-opendatahub-quota-slice"
	case ClusterProfileAWSDevfile:
		return "aws-devfile-quota-slice"
	case ClusterProfileAWSSPLAT:
		return "aws-splat-quota-slice"
	case ClusterProfileAWSKubeVirt:
		return "aws-kubevirt-quota-slice"
	case ClusterProfileAWSRHOAIQE:
		return "aws-rhoai-qe-quota-slice"
	case ClusterProfileAWSOVNPerfScale:
		return "aws-ovn-perfscale-quota-slice"
	case ClusterProfileAlibabaCloud:
		return "alibabacloud-quota-slice"
	case ClusterProfileAlibabaCloudQE:
		return "alibabacloud-qe-quota-slice"
	case ClusterProfileAlibabaCloudCNQE:
		return "alibabacloud-cn-qe-quota-slice"
	case ClusterProfileAzure2:
		return "azure-2-quota-slice"
	case ClusterProfileAzure4:
		return "azure4-quota-slice"
	case ClusterProfileAzureArm64:
		return "azure-arm64-quota-slice"
	case ClusterProfileAzureArc:
		return "azure-arc-quota-slice"
	case ClusterProfileAzurePerfScale:
		return "azure-perfscale-quota-slice"
	case ClusterProfileAzureStack:
		return "azurestack-quota-slice"
	case ClusterProfileAzureStackDEV:
		return "azurestack-dev-quota-slice"
	case ClusterProfileAzureStackQE:
		return "azurestack-qe-quota-slice"
	case ClusterProfileAWSOSDMSP:
		return "aws-osd-msp-quota-slice"
	case ClusterProfileAzureMag:
		return "azuremag-quota-slice"
	case ClusterProfileAzureQE:
		return "azure-qe-quota-slice"
	case ClusterProfileAzureObservability:
		return "azure-observability-quota-slice"
	case ClusterProfileAzureHCPQE:
		return "azure-hcp-qe-quota-slice"
	case ClusterProfileAzureHCPHAQE:
		return "azure-hcp-ha-qe-quota-slice"
	case ClusterProfileAzureAutoreleaseQE:
		return "azure-autorelease-qe-quota-slice"
	case ClusterProfileAzureMagQE:
		return "azuremag-qe-quota-slice"
	case ClusterProfileAzureArm64QE:
		return "azure-arm64-qe-quota-slice"
	case ClusterProfileAzureVirtualization:
		return "azure-virtualization-quota-slice"
	case ClusterProfileAzureSustAutoRel412:
		return "azure-sustaining-autorelease-412-quota-slice"
	case ClusterProfileAzureConfidentialQE:
		return "azure-confidential-qe-quota-slice"
	case ClusterProfileAzureCNVDevOps:
		return "azure-cnv-devops-quota-slice"
	case ClusterProfileEquinixOcpMetal:
		return "equinix-ocp-metal-quota-slice"
	case ClusterProfileEquinixOcpMetalQE:
		return "equinix-ocp-metal-qe-quota-slice"
	case ClusterProfileEquinixOcpHCP:
		return "equinix-ocp-hcp-quota-slice"
	case ClusterProfileFleetManagerQE:
		return "fleet-manager-qe-quota-slice"
	case ClusterProfileGCPQE:
		return "gcp-qe-quota-slice"
	case ClusterProfileGCPQEC3Metal:
		return "gcp-qe-c3-metal-quota-slice"
	case ClusterProfileGCPAutoReleaseQE:
		return "gcp-autorelease-qe-quota-slice"
	case ClusterProfileGCPArm64:
		return "gcp-arm64-quota-slice"
	case
		ClusterProfileGCP:
		return "gcp-quota-slice"
	case ClusterProfileGCP2:
		return "gcp-openshift-gce-devel-ci-2-quota-slice"
	case ClusterProfileGCP3:
		return "gcp-3-quota-slice"
	case ClusterProfileGCPVirtualization:
		return "gcp-virtualization-quota-slice"
	case ClusterProfileGCPOpendatahub:
		return "gcp-opendatahub-quota-slice"
	case ClusterProfileGCPTelco:
		return "gcp-telco-quota-slice"
	case ClusterProfileGCPSustAutoRel412:
		return "gcp-sustaining-autorelease-412-quota-slice"
	case ClusterProfileGCPChaos:
		return "gcp-chaos-quota-slice"
	case ClusterProfileGCPConfidentialQE:
		return "gcp-confidential-qe-slice"
	case ClusterProfileGCPPerfScaleQE:
		return "gcp-perfscale-qe-quota-slice"
	case ClusterProfileIBMCloud:
		return "ibmcloud-quota-slice"
	case ClusterProfileIBMCloudCSPIQE:
		return "ibmcloud-cspi-qe-quota-slice"
	case ClusterProfileIBMCloudQE:
		return "ibmcloud-qe-quota-slice"
	case ClusterProfileIBMCloudQE2:
		return "ibmcloud-qe-2-quota-slice"
	case ClusterProfileIBMCloudMultiPpc64le:
		return "ibmcloud-multi-ppc64le-quota-slice"
	case ClusterProfileIBMCloudMultiS390x:
		return "ibmcloud-multi-s390x-quota-slice"
	case ClusterProfilePOWERVC1:
		return "powervc-1-quota-slice"
	case ClusterProfilePOWERVSMulti1:
		return "powervs-multi-1-quota-slice"
	case ClusterProfilePOWERVS1:
		return "powervs-1-quota-slice"
	case ClusterProfilePOWERVS2:
		return "powervs-2-quota-slice"
	case ClusterProfilePOWERVS3:
		return "powervs-3-quota-slice"
	case ClusterProfilePOWERVS4:
		return "powervs-4-quota-slice"
	case ClusterProfilePOWERVS5:
		return "powervs-5-quota-slice"
	case ClusterProfilePOWERVS6:
		return "powervs-6-quota-slice"
	case ClusterProfilePOWERVS7:
		return "powervs-7-quota-slice"
	case ClusterProfilePOWERVS8:
		return "powervs-8-quota-slice"
	case ClusterProfilePOWERVS9:
		return "powervs-9-quota-slice"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le-quota-slice"
	case ClusterProfileLibvirtPpc64leS2S:
		return "libvirt-ppc64le-s2s-quota-slice"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x-quota-slice"
	case ClusterProfileLibvirtS390x1:
		return "libvirt-s390x-1-quota-slice"
	case ClusterProfileLibvirtS390x2:
		return "libvirt-s390x-2-quota-slice"
	case ClusterProfileLibvirtS390xAmd64:
		return "libvirt-s390x-amd64-quota-slice"
	case ClusterProfileLibvirtS390xVPN:
		return "libvirt-s390x-vpn-quota-slice"
	case ClusterProfileMetalPerfscaleBMCPT:
		return "metal-perfscale-cpt-quota-slice"
	case ClusterProfileMetalPerfscaleJetlag:
		return "metal-perfscale-jetlag-quota-slice"
	case ClusterProfileMetalPerfscaleOSP:
		return "metal-perfscale-osp-quota-slice"
	case ClusterProfileMetalPerfscaleSelfSched:
		return "metal-perfscale-selfsched-quota-slice"
	case ClusterProfileMetalPerfscaleTelco:
		return "metal-perfscale-telco-quota-slice"
	case ClusterProfileMetalTelco5G:
		return "metal-telco5g-quota-slice"
	case ClusterProfileMetalTelcoV10N:
		return "metal-telcov10n-quota-slice"
	case ClusterProfileMetalTelco5GPTP:
		return "metal-telco5g-ptp-quota-slice"
	case ClusterProfileNutanix:
		return "nutanix-quota-slice"
	case ClusterProfileNutanixQE:
		return "nutanix-qe-quota-slice"
	case ClusterProfileNutanixQEDis:
		return "nutanix-qe-dis-quota-slice"
	case ClusterProfileNutanixQEZone:
		return "nutanix-qe-zone-quota-slice"
	case ClusterProfileNutanixQEGPU:
		return "nutanix-qe-gpu-quota-slice"
	case ClusterProfileNutanixQEFlow:
		return "nutanix-qe-flow-quota-slice"
	case ClusterProfileOpenStackHwoffload:
		return "openstack-hwoffload-quota-slice"
	case ClusterProfileOpenStackIBMOSP:
		return "openstack-ibm-osp-quota-slice"
	case ClusterProfileOpenStackNFV:
		return "openstack-nfv-quota-slice"
	case ClusterProfileOpenStackMechaCentral:
		return "openstack-vh-mecha-central-quota-slice"
	case ClusterProfileOpenStackMechaAz0:
		return "openstack-vh-mecha-az0-quota-slice"
	case ClusterProfileOpenStackNercDev:
		return "openstack-nerc-dev-quota-slice"
	case ClusterProfileOpenStackRHOSO:
		return "openstack-rhoso-quota-slice"
	case ClusterProfileOpenStackRHOSCI:
		return "openstack-rhos-ci-quota-slice"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl-quota-slice"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost-quota-slice"
	case ClusterProfileOpenStackVexxhostRHOS:
		return "openstack-vh-bm-rhos-quota-slice"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le-quota-slice"
	case ClusterProfileOpenStackOpVexxhost:
		return "openstack-operators-vexxhost-quota-slice"
	case ClusterProfileOvirt:
		return "ovirt-quota-slice"
	case ClusterProfilePacket:
		return "packet-quota-slice"
	case
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO:
		return "packet-edge-quota-slice"
	case ClusterProfileVSphereDis2:
		return "vsphere-dis-2-quota-slice"
	case ClusterProfileVSphereMultizone2:
		return "vsphere-multizone-2-quota-slice"
	case ClusterProfileVSphereConnected2:
		return "vsphere-connected-2-quota-slice"
	case ClusterProfileVSphereElasticPoc:
		return "vsphere-elastic-poc-quota-slice"
	case ClusterProfileVSphereElastic:
		return "vsphere-elastic-quota-slice"
	case ClusterProfileKubevirt:
		return "kubevirt-quota-slice"
	case ClusterProfileAWSCPaaS:
		return "aws-cpaas-quota-slice"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral-quota-slice"
	case ClusterProfileAWS2:
		return "aws-2-quota-slice"
	case ClusterProfileAWS3:
		return "aws-3-quota-slice"
	case ClusterProfileAWS4:
		return "aws-4-quota-slice"
	case ClusterProfileAWS5:
		return "aws-5-quota-slice"
	case ClusterProfileOpenshiftOrgAWS:
		return "openshift-org-aws-quota-slice"
	case ClusterProfileOpenshiftOrgAzure:
		return "openshift-org-azure-quota-slice"
	case ClusterProfileOCIAgent:
		return "oci-agent-qe-quota-slice"
	case ClusterProfileOCIAssisted:
		return "oci-edge-quota-slice"
	case ClusterProfileHypershiftAWS:
		return "hypershift-aws-quota-slice"
	case ClusterProfileHypershiftAzure:
		return "hypershift-azure-quota-slice"
	case ClusterProfileHypershiftAKS:
		return "hypershift-aks-quota-slice"
	case ClusterProfileHypershiftPowerVS:
		return "hypershift-powervs-quota-slice"
	case ClusterProfileHypershiftPowerVSCB:
		return "hypershift-powervs-cb-quota-slice"
	case ClusterProfileHypershiftGCP:
		return "hypershift-gcp-quota-slice"
	case ClusterProfileOSSM:
		return "ossm-aws-quota-slice"
	case ClusterProfileAWSConfidentialQE:
		return "aws-confidential-qe-quota-slice"
	case ClusterProfileMedik8sAWS:
		return "medik8s-aws-quota-slice"
	case ClusterProfileGitOpsAWS:
		return "gitops-aws-quota-slice"
	case ClusterProfileCheAWS:
		return "che-aws-quota-slice"
	case ClusterProfileOSLGCP:
		return "osl-gcp-quota-slice"
	case ClusterProfileDevSandboxCIAWS:
		return "devsandboxci-aws-quota-slice"
	case ClusterProfileQuayAWS:
		return "quay-aws-quota-slice"
	case ClusterProfileAWSQUAYQE:
		return "aws-quay-qe-quota-slice"
	case ClusterProfileGCPQUAYQE:
		return "gcp-quay-qe-quota-slice"
	case ClusterProfileAzureQUAYQE:
		return "azure-quay-qe-quota-slice"
	case ClusterProfileAWSEdgeInfra:
		return "aws-edge-infra-quota-slice"
	case ClusterProfileRHOpenShiftEcosystem:
		return "rh-openshift-ecosystem-quota-slice"
	case ClusterProfileODFAWS:
		return "odf-aws-quota-slice"
	case ClusterProfileKonfluxWorkspacesAWS:
		return "konfluxworkspaces-aws-quota-slice"
	case ClusterProfileAWSObservabiltity:
		return "aws-observability-quota-slice"
	case ClusterProfileAWSSDCICD:
		return "aws-sd-cicd-quota-slice"
	case ClusterProfileGCPSDCICD:
		return "gcp-sd-cicd-quota-slice"
	case ClusterProfileAroRH:
		return "aro-redhat-tenant-quota-slice"
	case ClusterProfileAWSManagedRosaRHOAIQE:
		return "aws-managed-rosa-rhoai-qe-quota-slice"
	case ClusterProfileAWSManagedOSDRHOAIQE:
		return "aws-managed-osd-rhoai-qe-quota-slice"
	case ClusterProfileIBMCloudRHOAIQE:
		return "ibmcloud-rhoai-qe-quota-slice"
	case ClusterProfileGCPObservability:
		return "gcp-observability-quota-slice"
	case ClusterProfileEquinixEdgeEnablement:
		return "equinix-edge-enablement-quota-slice"
	case ClusterProfileAWSMCOQE:
		return "aws-mco-qe-quota-slice"
	case ClusterProfileAWSOADPQE:
		return "aws-oadp-qe-quota-slice"
	case ClusterProfileAzureOADPQE:
		return "azure-oadp-qe-quota-slice"
	case ClusterProfileGCPOADPQE:
		return "gcp-oadp-qe-quota-slice"
	case ClusterProfileAWSlpChaos:
		return "aws-lp-chaos-quota-slice"
	case ClusterProfileMetalRHgs:
		return "metal-redhat-gs-quota-slice"
	case ClusterProfileAWSOSPQE:
		return "aws-osp-qe-quota-slice"
	case ClusterProfileAWSOSC:
		return "aws-sandboxed-containers-operator-quota-slice"
	case ClusterProfileRosaRegionalPlatformInt:
		return "rosa-regional-platform-int-quota-slice"

	case ClusterProfileAROHCPInt:
		return "aro-hcp-int-quota-slice"
	case ClusterProfileAROHCPStg:
		return "aro-hcp-stg-quota-slice"
	case ClusterProfileAROHCPProd:
		return "aro-hcp-prod-quota-slice"
	case ClusterProfileAROHCPDev:
		return "aro-hcp-dev-quota-slice"

	case ClusterProfileAROClassicInt:
		return "aro-classic-int-quota-slice"
	case ClusterProfileAROClassicStg:
		return "aro-classic-stg-quota-slice"
	case ClusterProfileAROClassicProd:
		return "aro-classic-prod-quota-slice"
	case ClusterProfileAROClassicDev:
		return "aro-classic-dev-quota-slice"

	default:
		return ""
	}
}

func (p ClusterProfile) IPPoolLeaseType() string {
	switch p {
	case ClusterProfileAWS:
		return "aws-ip-pools"
	default:
		return ""
	}
}

// IPPoolLeaseShouldValidateBranch declares whether the ip-pool leases should only be applied to branches matching a
// specific OpenShift validation model. returns true by default, but should return false for any cluster-profiles
// that don't want this validation
func (p ClusterProfile) IPPoolLeaseShouldValidateBranch() bool {
	switch p {
	default:
		return true
	}
}

// GetDefaultClusterProfileSecretName returns the default secret name for the profile
func GetDefaultClusterProfileSecretName(profile ClusterProfile) string {
	return fmt.Sprintf("cluster-secrets-%s", string(profile))
}

// LeaseTypeFromClusterType maps cluster types to lease types
func LeaseTypeFromClusterType(t string) (string, error) {
	switch t {
	case
		"aws", "aws-c2s", "aws-china", "aws-usgov", "aws-sc2s", "aws-eusc", "aws-osd-msp", "aws-opendatahub", "aws-splat",
		"alibaba", "azure-2", "azure4", "azure-arc", "azure-arm64", "azurestack", "azuremag", "equinix-ocp-metal",
		"gcp", "gcp-arm64", "gcp-opendatahub", "libvirt-ppc64le", "libvirt-ppc64le-s2s", "libvirt-s390x",
		"libvirt-s390x-1", "libvirt-s390x-2", "libvirt-s390x-amd64", "libvirt-s390x-vpn", "ibmcloud-multi-ppc64le",
		"ibmcloud-multi-s390x", "nutanix", "nutanix-qe", "nutanix-qe-dis", "nutanix-qe-zone", "nutanix-qe-gpu",
		"nutanix-qe-flow", "openstack", "openstack-osuosl", "openstack-vexxhost", "openstack-ppc64le",
		"openstack-nerc-dev", "vsphere", "ovirt", "packet", "packet-edge", "powervc-1", "powervs-multi-1",
		"powervs-1", "powervs-2", "powervs-3", "powervs-4", "powervs-5", "powervs-6", "powervs-7", "powervs-8", "powervs-9",
		"kubevirt", "aws-cpaas", "osd-ephemeral", "gcp-virtualization", "aws-virtualization",
		"azure-virtualization", "hypershift-aws", "hypershift-aks", "hypershift-azure",
		"hypershift-powervs", "hypershift-powervs-cb", "hypershift-gcp", "aws-mco-qe",
		"equinix-edge-enablement", "aws-oadp-qe", "azure-oadp-qe", "gcp-oadp-qe", "aws-lp-chaos", "aws-osp-qe",
		"metal-redhat-gs", "aro-hcp-int", "aro-hcp-stg", "aro-hcp-prod", "aro-hcp-dev", "rosa-regional-platform-int", "hyperfleet-e2e",
		"aro-classic-int", "aro-classic-stg", "aro-classic-prod", "aro-classic-dev", "rosa-e2e-01", "rosa-e2e-02", "rosa-e2e-03":
		return t + "-quota-slice", nil
	default:
		return "", fmt.Errorf("invalid cluster type %q", t)
	}
}

// ClusterTestConfiguration describes a test that provisions
// a cluster and runs a command in it.
type ClusterTestConfiguration struct {
	ClusterProfile ClusterProfile `json:"cluster_profile"`
}

// PipelineImageStreamTagReference is a tag on the
// ImageStream corresponding to the code under test.
// This tag will identify an image but not use any
// namespaces or prefixes, For instance, if for the
// image openshift/origin-pod, the tag would be `pod`.
type PipelineImageStreamTagReference string

const (
	PipelineImageStreamTagReferenceRoot         PipelineImageStreamTagReference = "root"
	PipelineImageStreamTagReferenceSource       PipelineImageStreamTagReference = "src"
	PipelineImageStreamTagReferenceBinaries     PipelineImageStreamTagReference = "bin"
	PipelineImageStreamTagReferenceTestBinaries PipelineImageStreamTagReference = "test-bin"
	PipelineImageStreamTagReferenceRPMs         PipelineImageStreamTagReference = "rpms"
)

// The fields in ReleaseBuildConfiguration which originate each pipeline image
const (
	PipelineImageStreamTagSourceRoot         = "build_root"
	PipelineImageStreamTagSourceBinaries     = "binary_build_commands"
	PipelineImageStreamTagSourceTestBinaries = "test_binary_build_commands"
	PipelineImageStreamTagSourceRPMs         = "rpm_build_commands"
)

const (
	ClonerefsPullSpec string = "quay-proxy.ci.openshift.org/openshift/ci:ci_clonerefs_latest"
	ClonerefsPath     string = "/ko-app/clonerefs"
)

// SourceStepConfiguration describes a step that
// clones the source repositories required for
// jobs. If no output tag is provided, the default
// of `src` is used.
type SourceStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to,omitempty"`

	// ClonerefsPullSpec is the image pullSpec where we get the clonerefs tool
	ClonerefsPullSpec string `json:"pull_spec,omitempty"`
	// ClonerefsPath is the path in the above image where the
	// clonerefs tool is placed
	ClonerefsPath string `json:"clonerefs_path"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`
}

func (config SourceStepConfiguration) TargetName() string {
	return string(config.To)
}

// OperatorStepConfiguration describes the locations of operator bundle information,
// bundle build dockerfiles, and images the operator(s) depends on that must
// be substituted to run in a CI test cluster
type OperatorStepConfiguration struct {
	// Bundles define a dockerfile and build context to build a bundle
	Bundles []Bundle `json:"bundles,omitempty"`

	// Substitutions describes the pullspecs in the operator manifests that must be subsituted
	// with the pull specs of the images in the CI registry
	Substitutions []PullSpecSubstitution `json:"substitutions,omitempty"`
}

// IndexUpdate specifies the update mode for an operator being added to an index
type IndexUpdate string

const (
	IndexUpdateSemver          = "semver"
	IndexUpdateReplaces        = "replaces"
	IndexUpdateSemverSkippatch = "semver-skippatch"
)

// Bundle contains the data needed to build a bundle from the bundle source image and update an index to include the new bundle
type Bundle struct {
	// As defines the name for this bundle. If not set, a name will be automatically generated for the bundle.
	As string `json:"as,omitempty"`
	// DockerfilePath defines where the dockerfile for build the bundle exists relative to the contextdir
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// ContextDir defines the source directory to build the bundle from relative to the repository root
	ContextDir string `json:"context_dir,omitempty"`
	// BaseIndex defines what index image to use as a base when adding the bundle to an index
	BaseIndex string `json:"base_index,omitempty"`
	// UpdateGraph defines the update mode to use when adding the bundle to the base index.
	// Can be: semver (default), semver-skippatch, or replaces
	UpdateGraph IndexUpdate `json:"update_graph,omitempty"`
	// Skip building the index image for this bundle. Default to false.
	// This field works only for named bundles, i.e., "as" is not empty.
	SkipBuildingIndex bool `json:"skip_building_index,omitempty"`
	// Optional indicates that the job's status context, that is generated from the corresponding test, should not be required for merge.
	Optional bool `json:"optional,omitempty"`
	// Capabilities is the list of strings that define additional capabilities needed by the bundle build job
	Capabilities []string `json:"capabilities,omitempty"`
}

// IndexGeneratorStepConfiguration describes a step that creates an index database and
// Dockerfile to build an operator index that uses the generated database based on
// bundle names provided in OperatorIndex
type IndexGeneratorStepConfiguration struct {
	To PipelineImageStreamTagReference `json:"to,omitempty"`

	// OperatorIndex is a list of the names of the bundle images that the
	// index will contain in its database.
	OperatorIndex []string `json:"operator_index,omitempty"`

	// BaseIndex is the index image to add the bundle(s) to. If unset, a new index is created
	BaseIndex string `json:"base_index,omitempty"`

	// UpdateGraph defines the mode to us when updating the index graph
	UpdateGraph IndexUpdate `json:"update_graph,omitempty"`
}

func (config IndexGeneratorStepConfiguration) TargetName() string {
	return string(config.To)
}

// PipelineImageStreamTagReferenceIndexImageGenerator is the name of the index image generator built by ci-operator
const PipelineImageStreamTagReferenceIndexImageGenerator PipelineImageStreamTagReference = "ci-index-gen"

// PipelineImageStreamTagReferenceIndexImage is the name of the index image built by ci-operator
const PipelineImageStreamTagReferenceIndexImage PipelineImageStreamTagReference = "ci-index"

func IsIndexImage(imageName string) bool {
	return strings.HasPrefix(imageName, string(PipelineImageStreamTagReferenceIndexImage))
}

func IndexName(bundleName string) string {
	return fmt.Sprintf("%s-%s", PipelineImageStreamTagReferenceIndexImage, bundleName)
}

func IndexGeneratorName(indexName PipelineImageStreamTagReference) PipelineImageStreamTagReference {
	return PipelineImageStreamTagReference(fmt.Sprintf("%s-gen", indexName))
}

// BundleSourceStepConfiguration describes a step that performs a set of
// substitutions on all yaml files in the `src` image so that the
// pullspecs in the operator manifests point to images inside the CI registry.
// It is intended to be used as the source image for bundle image builds.
type BundleSourceStepConfiguration struct {
	// Substitutions contains pullspecs that need to be replaced by images
	// in the CI cluster for operator bundle images
	Substitutions []PullSpecSubstitution `json:"substitutions,omitempty"`
}

func (config BundleSourceStepConfiguration) TargetName() string {
	return string(PipelineImageStreamTagReferenceBundleSource)
}

// PipelineImageStreamTagReferenceBundleSourceName is the name of the bundle source image built by the CI
const PipelineImageStreamTagReferenceBundleSource PipelineImageStreamTagReference = "src-bundle"

// BundlePrefix is the prefix used by ci-operator for bundle images without an explicitly configured name
const BundlePrefix = "ci-bundle"

func (config ReleaseBuildConfiguration) IsBundleImage(imageName string) bool {
	if config.Operator == nil {
		return false
	}
	if strings.HasPrefix(imageName, BundlePrefix) {
		return true
	}
	for _, bundle := range config.Operator.Bundles {
		if bundle.As != "" && imageName == bundle.As {
			return true
		}
	}
	return false
}

func BundleName(index int) string {
	return fmt.Sprintf("%s%d", BundlePrefix, index)
}

// ImageConfiguration holds the configuration for building images
// along with the run conditions for the auto-generated images job.
type ImageConfiguration struct {
	// RunIfChanged is a regex that will cause the auto-generated images
	// presubmit and postsubmit to only run if a file matching the regex is changed.
	RunIfChanged string `json:"run_if_changed,omitempty"`

	// SkipIfOnlyChanged is a regex that will cause the auto-generated images
	// presubmit and postsubmit to be skipped if all changed files match the regex.
	SkipIfOnlyChanged string `json:"skip_if_only_changed,omitempty"`

	// PipelineRunIfChanged is a regex that will cause the auto-generated images
	// presubmit to only run in the second stage of the pipeline if a matching file is changed.
	PipelineRunIfChanged string `json:"pipeline_run_if_changed,omitempty"`

	// PipelineSkipIfOnlyChanged is a regex that will cause the auto-generated images
	// presubmit to be skipped in the second stage of the pipeline if all changed files match the regex.
	PipelineSkipIfOnlyChanged string `json:"pipeline_skip_if_only_changed,omitempty"`

	// BuildIfAffected means images are only built if their corresponding cmd tools are affected by code changes.
	// When enabled, the tool-detector package analyzes git changes to determine which images should be built.
	// The image name (To field) should match the cmd tool name for this to work correctly.
	BuildIfAffected bool `json:"build_if_affected,omitempty"`

	// Items is the list of images to build.
	Items []ProjectDirectoryImageBuildStepConfiguration `json:"items,omitempty"`
}

// TODO: Remove custom UnmarshalJSON after migration from plain list to ImageConfiguration struct is complete.

func (ic *ImageConfiguration) UnmarshalJSON(data []byte) error {
	var items []ProjectDirectoryImageBuildStepConfiguration
	if err := json.Unmarshal(data, &items); err == nil {
		ic.Items = items
		return nil
	}
	type rawImageConfiguration ImageConfiguration
	return json.Unmarshal(data, (*rawImageConfiguration)(ic))
}

// ProjectDirectoryImageBuildStepConfiguration describes an
// image build from a directory in a component project.
type ProjectDirectoryImageBuildStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from,omitempty"`
	To   PipelineImageStreamTagReference `json:"to"`

	ProjectDirectoryImageBuildInputs `json:",inline"`

	// Optional means the build step is not built, published, or
	// promoted unless explicitly targeted. Use for builds which
	// are invoked only when testing certain parts of the repo.
	Optional bool `json:"optional,omitempty"`

	// MultiArch means the build step is built for multiple architectures if available. Defaults to false.
	// DEPRECATED: use AdditionalArchitectures instead
	MultiArch bool `json:"multi_arch,omitempty"`

	// AdditionalArchitectures is a list of additional architectures to build for. AMD64 architecture is included by default.
	AdditionalArchitectures []string `json:"additional_architectures,omitempty"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`

	// isBundleImage indicates that this build step is a bundle image
	isBundleImage bool
}

func (config ProjectDirectoryImageBuildStepConfiguration) TargetName() string {
	return string(config.To)
}

// IsBundleImage returns the value of the isBundleImage field
func (p *ProjectDirectoryImageBuildStepConfiguration) IsBundleImage() bool {
	return p.isBundleImage
}

// WithBundleImage sets the isBundleImage field to the provided value
func (p *ProjectDirectoryImageBuildStepConfiguration) WithBundleImage(isBundleImage bool) *ProjectDirectoryImageBuildStepConfiguration {
	p.isBundleImage = isBundleImage
	return p
}

// ProjectDirectoryImageBuildInputs holds inputs for an image build from the repo under test
type ProjectDirectoryImageBuildInputs struct {
	// ContextDir is the directory in the project
	// from which this build should be run.
	ContextDir string `json:"context_dir,omitempty"`

	// DockerfilePath is the path to a Dockerfile in the
	// project to run relative to the context_dir.
	DockerfilePath string `json:"dockerfile_path,omitempty"`

	// DockerfileLiteral can be used to  provide an inline Dockerfile.
	// Mutually exclusive with DockerfilePath.
	DockerfileLiteral *string `json:"dockerfile_literal,omitempty"`

	// Inputs is a map of tag reference name to image input changes
	// that will populate the build context for the Dockerfile or
	// alter the input image for a multi-stage build.
	Inputs map[string]ImageBuildInputs `json:"inputs,omitempty"`

	// BuildArgs contains build arguments that will be resolved in the Dockerfile.
	// See https://docs.docker.com/engine/reference/builder/#/arg for more details.
	BuildArgs []BuildArg `json:"build_args,omitempty"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`
}

type BuildArg struct {
	// Name of the build arg.
	Name string `json:"name,omitempty"`

	// Value of the build arg.
	Value string `json:"value,omitempty"`
}

// PullSpecSubstitution contains a name of a pullspec that needs to
// be substituted with the name of a different pullspec. This is used
// for generated operator bundle images.
type PullSpecSubstitution struct {
	// PullSpec is the pullspec that needs to be replaced
	PullSpec string `json:"pullspec,omitempty"`
	// With is the string that the PullSpec is being replaced by
	With string `json:"with,omitempty"`
}

// ImageBuildInputs is a subset of the v1 OpenShift Build API object
// defining an input source.
type ImageBuildInputs struct {
	// Paths is a list of paths to copy out of this image and into the
	// context directory.
	Paths []ImageSourcePath `json:"paths,omitempty"`
	// As is a list of multi-stage step names or image names that will
	// be replaced by the image reference from this step. For instance,
	// if the Dockerfile defines FROM nginx:latest AS base, specifying
	// either "nginx:latest" or "base" in this array will replace that
	// image with the pipeline input.
	As []string `json:"as,omitempty"`
}

// ImageSourcePath maps a path in the source image into a destination
// path in the context. See the v1 OpenShift Build API for more info.
type ImageSourcePath struct {
	// SourcePath is a file or directory in the source image to copy from.
	SourcePath string `json:"source_path"`
	// DestinationDir is the directory in the destination image to copy
	// to.
	DestinationDir string `json:"destination_dir"`
}

// RPMImageInjectionStepConfiguration describes a step
// that updates injects an RPM repo into an image. If no
// output tag is provided, the input tag is updated.
type RPMImageInjectionStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to,omitempty"`
}

func (config RPMImageInjectionStepConfiguration) TargetName() string {
	return string(config.To)
}

// RPMServeStepConfiguration describes a step that launches
// a server from an image with RPMs and exposes it to the web.
type RPMServeStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`

	// Ref is an optional string linking to the extra_ref in "org.repo" format that this belongs to
	Ref string `json:"ref,omitempty"`
}

func (config RPMServeStepConfiguration) TargetName() string {
	if config.Ref != "" {
		return fmt.Sprintf("[serve:rpms-%s]", config.Ref)
	}
	return "[serve:rpms]"
}

const (
	// PipelineImageStream is the name of the
	// ImageStream used to hold images built
	// to cache build steps in the pipeline.
	PipelineImageStream = "pipeline"

	// DefaultRPMLocation is the default relative
	// directory for Origin-based projects to put
	// their built RPMs.
	DefaultRPMLocation = "_output/local/releases/rpms/"

	// RPMServeLocation is the location from which
	// we will serve RPMs after they are built.
	RPMServeLocation = "/srv/repo"

	// StableImageStream is the ImageStream used to hold
	// build outputs from the repository under test and
	// the associated images imported from integration streams
	StableImageStream = "stable"
	// LatestReleaseName is the name of the special latest
	// stable stream, images in this stream are held in
	// the StableImageStream. Images for other versions of
	// the stream are held in similarly-named streams.
	LatestReleaseName = "latest"
	// InitialReleaseName is the name of the special initial
	// stream we copy at import to keep for upgrade tests.
	// TODO(skuznets): remove these when they're not implicit
	InitialReleaseName = "initial"

	// ReleaseImageStream is the name of the ImageStream
	// used to hold built or imported release payload images
	ReleaseImageStream = "release"

	ComponentFormatReplacement = "${component}"
)

type MetadataWithTest struct {
	Metadata `json:",inline"`
	Test     string `json:"test,omitempty"`
}

func (m *MetadataWithTest) JobName(prefix string) string {
	return m.Metadata.JobName(prefix, m.Test)
}

type ClusterProfilesList []ClusterProfileDetails
type ClusterProfilesMap map[ClusterProfile]ClusterProfileDetails

type ClusterProfileDetails struct {
	Profile     ClusterProfile         `yaml:"profile" json:"profile"`
	Owners      []ClusterProfileOwners `yaml:"owners,omitempty" json:"owners,omitempty"`
	ClusterType string                 `yaml:"cluster_type,omitempty" json:"cluster_type,omitempty"`
	LeaseType   string                 `yaml:"lease_type,omitempty" json:"lease_type,omitempty"`
	Secret      string                 `yaml:"secret,omitempty" json:"secret,omitempty"`
	ConfigMap   string                 `yaml:"config_map,omitempty" json:"config_map,omitempty"`
}

type ClusterProfileOwners struct {
	Org   string   `yaml:"org" json:"org"`
	Repos []string `yaml:"repos,omitempty" json:"repos,omitempty"`
}
type ClusterClaimOwnersMap map[string]ClusterClaimDetails

type ClusterClaimDetails struct {
	Claim  string                     `yaml:"claim"`
	Owners []ClusterClaimOwnerDetails `yaml:"owners,omitempty"`
}

type ClusterClaimOwnerDetails struct {
	Org   string   `yaml:"org"`
	Repos []string `yaml:"repos,omitempty"`
}

const (
	EphemeralClusterTestDoneSignalSecretName = "test-done-signal"
)
