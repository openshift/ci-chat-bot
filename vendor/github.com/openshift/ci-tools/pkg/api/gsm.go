package api

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	// DPTPGSMCollection is the default GSM collection for DPTP-managed secrets (dockerconfig items)
	DPTPGSMCollection = "test-platform-infra"
)

// GSMConfig is the top-level configuration for GSM-based secrets
type GSMConfig struct {
	ClusterGroups  map[string][]string       `json:"cluster_groups,omitempty"`
	DPTPCollection string                    `json:"dptp_collection,omitempty"`
	Components     map[string][]GSMSecretRef `json:"components,omitempty"`
	Bundles        []GSMBundle               `json:"bundles,omitempty"`
}

// GSMBundle defines a logical group of GSM secrets
type GSMBundle struct {
	Name          string            `json:"name"`
	Components    []string          `json:"components,omitempty"`
	DockerConfig  *DockerConfigSpec `json:"dockerconfig,omitempty"`
	GSMSecrets    []GSMSecretRef    `json:"gsm_secrets,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	SyncToCluster bool              `json:"sync_to_cluster,omitempty"`
	Targets       []TargetSpec      `json:"targets,omitempty"`
}

type GSMSecretRef struct {
	Collection string       `json:"collection"`
	Group      string       `json:"group"`
	Fields     []FieldEntry `json:"fields,omitempty"` // Encoded names as they appear in GSM
}

// FieldEntry can be either a simple string or an object with 'as' field
type FieldEntry struct {
	Name string `json:"name,omitempty"`
	As   string `json:"as,omitempty"`
}

// UnmarshalJSON allows SecretEntry to accept both:
//   - "secret-name"                   (simple string)
//   - {name: "token", as: "renamed"}  (object)
func (s *FieldEntry) UnmarshalJSON(data []byte) error {
	// Try parsing as a simple string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Name = str
		return nil
	}

	// Otherwise, parse as an object with name/as fields
	type Alias FieldEntry
	aux := &struct{ *Alias }{Alias: (*Alias)(s)}
	return json.Unmarshal(data, aux)
}

// DockerConfigSpec defines how to construct a DockerConfig json from GSM secrets
type DockerConfigSpec struct {
	As         string             `json:"as,omitempty"`
	Registries []RegistryAuthData `json:"registries"`
}

// RegistryAuthData specifies registry credentials
// Collection is always DPTPGSMCollection, which matches dptp_collection in the GSM config
type RegistryAuthData struct {
	Group       string `json:"group"`
	RegistryURL string `json:"registry_url"`
	AuthField   string `json:"auth_field"`
	EmailField  string `json:"email_field,omitempty"`
}

// TargetSpec defines where a bundle should be synced
type TargetSpec struct {
	ClusterGroups []string          `json:"cluster_groups,omitempty"`
	Cluster       string            `json:"cluster,omitempty"`
	Namespace     string            `json:"namespace"`
	Type          corev1.SecretType `json:"type,omitempty"`
}

// LoadGSMConfigFromFile loads a GSMConfig from a YAML file
func LoadGSMConfigFromFile(file string, config *GSMConfig) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return fmt.Errorf("couldn't read GSM config file: %w", err)
	}
	return yaml.UnmarshalStrict(bytes, config)
}

// LoadGSMProjectConfigFromFile loads a GSM project configuration from a YAML file
func LoadGSMProjectConfigFromFile(file string, config *gsm.Config) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return fmt.Errorf("couldn't read GSM project config file: %w", err)
	}
	if err := yaml.UnmarshalStrict(bytes, config); err != nil {
		return err
	}
	if strings.TrimSpace(config.ProjectIdString) == "" || strings.TrimSpace(config.ProjectIdNumber) == "" {
		return fmt.Errorf("GSM project config must define non-empty GCP_PROJECT_ID and GCP_PROJECT_NUMBER")
	}
	return nil
}

func (c *GSMConfig) UnmarshalJSON(d []byte) error {
	type Alias GSMConfig
	aux := (*Alias)(c)
	if err := json.Unmarshal(d, aux); err != nil {
		return err
	}
	return c.resolve()
}

func (c *GSMConfig) resolve() error {
	var errs []error

	if c.DPTPCollection == "" {
		for _, bundle := range c.Bundles {
			if bundle.DockerConfig != nil {
				c.DPTPCollection = DPTPGSMCollection
				break
			}
		}
	}

	// Expand cluster_groups to concrete cluster names
	for bundleIdx := range c.Bundles {
		bundle := &c.Bundles[bundleIdx]
		var newBundleTargets []TargetSpec

		for targetIdx, target := range bundle.Targets {
			if target.Cluster != "" && len(target.ClusterGroups) != 0 {
				errs = append(errs, fmt.Errorf("bundle %d target %d has both cluster and cluster_groups set, those are mutually exclusive", bundleIdx, targetIdx))
				continue
			}

			// Default Type to Opaque if not set
			if target.Type == "" {
				target.Type = corev1.SecretTypeOpaque
			}

			if target.Cluster != "" {
				newBundleTargets = append(newBundleTargets, target)
				continue
			}

			// Expand cluster groups - inherit Type from the cluster_group definition
			for _, clusterGroupName := range target.ClusterGroups {
				clusters, groupExists := c.ClusterGroups[clusterGroupName]
				if !groupExists {
					errs = append(errs, fmt.Errorf("bundle %d target %d references non-existent cluster_group %s", bundleIdx, targetIdx, clusterGroupName))
					continue
				}

				for _, cluster := range clusters {
					newBundleTargets = append(newBundleTargets, TargetSpec{
						Cluster:   cluster,
						Namespace: target.Namespace,
						Type:      target.Type, // Inherit Type (already defaulted to Opaque above)
					})
				}
			}
		}

		bundle.Targets = newBundleTargets
	}

	// Resolve component references into gsm_secrets
	for bundleIdx := range c.Bundles {
		bundle := &c.Bundles[bundleIdx]

		if len(bundle.Components) == 0 {
			continue
		}

		for _, componentName := range bundle.Components {
			componentSecrets, exists := c.Components[componentName]
			if !exists {
				errs = append(errs, fmt.Errorf("bundle %s references non-existent component: %s", bundle.Name, componentName))
				continue
			}

			// Append component's GSMSecretRefs to bundle's GSMSecrets
			bundle.GSMSecrets = append(bundle.GSMSecrets, componentSecrets...)
		}

		// Clear components after resolution
		bundle.Components = nil
	}

	// Expand ${CLUSTER} variable substitution
	var expandedBundles []GSMBundle
	for _, bundle := range c.Bundles {
		// Check if bundle contains ${CLUSTER} in any secret names
		hasClusterVar := false
		for _, gsmSecretRef := range bundle.GSMSecrets {
			for _, secret := range gsmSecretRef.Fields {
				if strings.Contains(secret.Name, "${CLUSTER}") {
					hasClusterVar = true
					break
				}
			}
			if hasClusterVar {
				break
			}
		}

		if !hasClusterVar {
			// No ${CLUSTER} substitution needed, keep bundle as-is
			expandedBundles = append(expandedBundles, bundle)
			continue
		}

		// Collect unique clusters from targets
		clusterTargets := make(map[string][]TargetSpec)
		for _, target := range bundle.Targets {
			clusterTargets[target.Cluster] = append(clusterTargets[target.Cluster], target)
		}

		// Validate that bundles using ${CLUSTER} have resolvable targets
		if len(clusterTargets) == 0 {
			errs = append(errs, fmt.Errorf("bundle %q uses ${CLUSTER} variable substitution but has no resolvable targets (check that cluster_groups or cluster references are valid)", bundle.Name))
			continue
		}

		// Create one bundle per unique cluster
		for cluster, targets := range clusterTargets {
			expandedBundle := GSMBundle{
				Name:          bundle.Name,
				Components:    nil, // Already resolved in phase 2
				DockerConfig:  bundle.DockerConfig,
				Labels:        bundle.Labels,
				SyncToCluster: bundle.SyncToCluster,
				Targets:       targets,
			}

			// Substitute ${CLUSTER} in GSMSecrets
			for _, gsmSecretRef := range bundle.GSMSecrets {
				expandedSecretRef := GSMSecretRef{
					Collection: gsmSecretRef.Collection,
					Group:      gsmSecretRef.Group,
					Fields:     make([]FieldEntry, len(gsmSecretRef.Fields)),
				}
				for i, secret := range gsmSecretRef.Fields {
					expandedSecretRef.Fields[i] = FieldEntry{
						Name: strings.ReplaceAll(secret.Name, "${CLUSTER}", cluster),
						As:   secret.As,
					}
				}
				expandedBundle.GSMSecrets = append(expandedBundle.GSMSecrets, expandedSecretRef)
			}
			expandedBundles = append(expandedBundles, expandedBundle)
		}
	}
	c.Bundles = expandedBundles

	return utilerrors.NewAggregate(errs)
}

type bundleKey struct {
	name      string
	cluster   string
	namespace string
}

// Validate checks the GSMConfig for internal consistency.
// Expects a resolved config (resolve() is called automatically during unmarshaling).
func (c *GSMConfig) Validate() error {
	var errs []error

	// Validate that dptp_collection is set if any bundle uses dockerconfig
	hasDockerConfig := false
	for _, bundle := range c.Bundles {
		if bundle.DockerConfig != nil {
			hasDockerConfig = true
			break
		}
	}
	if hasDockerConfig && c.DPTPCollection == "" {
		errs = append(errs, fmt.Errorf("dptp_collection must be set when bundles use dockerconfig"))
	}
	if c.DPTPCollection != "" && !gsmvalidation.ValidateCollectionName(c.DPTPCollection) {
		errs = append(errs, fmt.Errorf("dptp_collection has invalid collection name: %s", c.DPTPCollection))
	}

	// Validate components
	componentNames := make(map[string]bool)

	for componentName, gsmSecretRefs := range c.Components {
		if componentName == "" {
			errs = append(errs, fmt.Errorf("component has empty name"))
			continue
		}

		if componentNames[componentName] {
			errs = append(errs, fmt.Errorf("duplicate component name: %s", componentName))
		}
		componentNames[componentName] = true

		if len(gsmSecretRefs) == 0 {
			errs = append(errs, fmt.Errorf("component %s has no GSM secret references", componentName))
			continue
		}

		for j, gsmSecretRef := range gsmSecretRefs {
			if gsmSecretRef.Collection == "" {
				errs = append(errs, fmt.Errorf("component %s[%d] has empty collection", componentName, j))
			}
			if !gsmvalidation.ValidateCollectionName(gsmSecretRef.Collection) {
				errs = append(errs, fmt.Errorf("component %s[%d] has invalid collection string", componentName, j))
			}
			if !gsmvalidation.ValidateGroupName(gsmSecretRef.Group) {
				errs = append(errs, fmt.Errorf("component %s[%d] has invalid group string", componentName, j))
			}
			if len(gsmSecretRef.Fields) == 0 && gsmSecretRef.Group == "" {
				errs = append(errs, fmt.Errorf("component %s[%d] has neither group nor any fields defined", componentName, j))
			}
			for k, secret := range gsmSecretRef.Fields {
				if secret.Name == "" {
					errs = append(errs, fmt.Errorf("component %s[%d].secrets[%d] has empty name", componentName, j, k))
				}
				if !gsmvalidation.ValidateSecretName(secret.Name) {
					errs = append(errs, fmt.Errorf("component %s[%d].secrets[%d] has invalid name", componentName, j, k))
				}
				if secret.As != "" {
					if err := gsmvalidation.ValidateMountFileName(secret.As); err != nil {
						errs = append(errs, fmt.Errorf("component %s[%d].secrets[%d].as is invalid: %w", componentName, j, k, err))
					}
				}
			}
		}
	}

	// Check for duplicate bundles by name+cluster+namespace combination
	seenBundles := make(map[bundleKey]bool)

	for i, bundle := range c.Bundles {
		if bundle.Name == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] has empty name", i))
			continue
		}

		for _, target := range bundle.Targets {
			key := bundleKey{
				name:      bundle.Name,
				cluster:   target.Cluster,
				namespace: target.Namespace,
			}
			if seenBundles[key] {
				errs = append(errs, fmt.Errorf(
					"duplicate bundle: name=%s cluster=%s namespace=%s",
					bundle.Name, target.Cluster, target.Namespace))
			}
			seenBundles[key] = true
		}
		if err := validateBundle(&bundle, i); err != nil {
			errs = append(errs, err)
		}
	}

	// Check for duplicate bundle names ONLY for CSI bundles (sync_to_cluster: false)
	// K8s Secret bundles (sync_to_cluster: true) can have duplicate names across clusters
	seenCSIBundleNames := make(map[string]bool)
	for _, bundle := range c.Bundles {
		if bundle.SyncToCluster {
			continue
		}
		if seenCSIBundleNames[bundle.Name] {
			errs = append(errs, fmt.Errorf(
				"duplicate bundle name '%s' - bundles with sync_to_cluster: false must have unique names",
				bundle.Name))
		}
		seenCSIBundleNames[bundle.Name] = true
	}

	return utilerrors.NewAggregate(errs)
}

func validateBundle(bundle *GSMBundle, idx int) error {
	var errs []error

	if bundle.SyncToCluster && len(bundle.Targets) == 0 {
		errs = append(errs, fmt.Errorf("bundle %s has sync_to_cluster: true but no targets", bundle.Name))
	}
	if !bundle.SyncToCluster && len(bundle.Targets) > 0 {
		errs = append(errs, fmt.Errorf("bundle %s has sync_to_cluster: false but has targets (targets only apply when sync_to_cluster: true)", bundle.Name))
	}

	for j, target := range bundle.Targets {
		if target.Namespace == "" {
			errs = append(errs, fmt.Errorf("bundle %s target[%d] has empty namespace", bundle.Name, j))
		}
		// At this point, target.Cluster should always be set to a concrete cluster name.
		// If it's empty, cluster_groups resolution failed or target was misconfigured.
		if target.Cluster == "" {
			errs = append(errs, fmt.Errorf("bundle %s target[%d] has empty cluster (should have been resolved by this point)", bundle.Name, j))
		}
		// Allow empty type (will be defaulted by resolve()) or the two supported types
		if target.Type != "" && target.Type != corev1.SecretTypeOpaque && target.Type != corev1.SecretTypeDockerConfigJson {
			errs = append(errs, fmt.Errorf("bundle %s target[%d] has invalid type (%s)", bundle.Name, j, target.Type))
		}
	}

	for j, gsmSecret := range bundle.GSMSecrets {
		if gsmSecret.Collection == "" {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has empty collection", bundle.Name, j))
		}
		if !gsmvalidation.ValidateCollectionName(gsmSecret.Collection) {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has invalid collection string", bundle.Name, j))
		}
		if !gsmvalidation.ValidateGroupName(gsmSecret.Group) {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has invalid group string", bundle.Name, j))
		}
		if len(gsmSecret.Fields) == 0 && gsmSecret.Group == "" {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has no secrets and no group defined", bundle.Name, j))
		}
		for k, field := range gsmSecret.Fields {
			if field.Name == "" {
				errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d].secrets[%d] has empty name", bundle.Name, j, k))
			}
			if !gsmvalidation.ValidateSecretName(field.Name) {
				errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d].secrets[%d] has invalid name", bundle.Name, j, k))
			}
			if field.As != "" {
				if err := gsmvalidation.ValidateMountFileName(field.As); err != nil {
					errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d].fields[%d].as is invalid: %w", bundle.Name, j, k, err))
				}
			}
		}
	}

	if bundle.DockerConfig != nil {
		if !bundle.SyncToCluster {
			errs = append(errs, fmt.Errorf("bundle %s has dockerconfig but sync_to_cluster is false - dockerconfig bundles must have sync_to_cluster: true", bundle.Name))
		}
		if err := validateDockerConfig(bundle.DockerConfig, idx, bundle.Name); err != nil {
			errs = append(errs, err)
		}
	}

	for i, label := range bundle.Labels {
		key, _, err := ParseLabel(label)
		if err != nil {
			errs = append(errs, fmt.Errorf("bundle %s labels[%d]: %w", bundle.Name, i, err))
		} else if key == DPTPRequesterLabel {
			errs = append(errs, fmt.Errorf("bundle %s labels[%d]: %q is a reserved label key", bundle.Name, i, DPTPRequesterLabel))
		}
	}

	if len(bundle.GSMSecrets) == 0 && bundle.DockerConfig == nil && len(bundle.Components) == 0 {
		errs = append(errs, fmt.Errorf("bundle %s has neither gsm_secrets, dockerconfig, nor components", bundle.Name))
	}

	return utilerrors.NewAggregate(errs)
}

func validateDockerConfig(dc *DockerConfigSpec, bundleIdx int, bundleName string) error {
	var errs []error

	if dc.As != "" {
		if err := gsmvalidation.ValidateMountFileName(dc.As); err != nil {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig.as is invalid: %w", bundleIdx, bundleName, err))
		}
	}

	if len(dc.Registries) == 0 {
		errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig has no registries", bundleIdx, bundleName))
	}

	for i, reg := range dc.Registries {
		if !gsmvalidation.ValidateGroupName(reg.Group) {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has invalid group string", bundleIdx, bundleName, i))
		}
		if reg.RegistryURL == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has empty registry_url", bundleIdx,
				bundleName, i))
		}
		if !gsmvalidation.ValidateSecretName(reg.AuthField) {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has empty auth_field", bundleIdx,
				bundleName, i))
		}
	}

	return utilerrors.NewAggregate(errs)
}
