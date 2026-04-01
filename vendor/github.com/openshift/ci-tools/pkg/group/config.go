package group

import (
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	validation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

const (
	// OpenshiftPrivAdminsGroup defines the group that will be used for the openshift-priv namespace in the app.ci cluster.
	OpenshiftPrivAdminsGroup = "openshift-priv-admins"
)

// Config represents the configuration file for the groups
type Config struct {
	// ClusterGroups holds the mapping from cluster group name to the clusters in the group
	ClusterGroups map[string][]string `json:"cluster_groups,omitempty" yaml:"cluster_groups,omitempty"`
	// Groups holds the mapping from group name to its target
	Groups map[string]Target `json:"groups,omitempty" yaml:"groups,omitempty"`
}

// Target represents the distribution of a group
// If neither Clusters and nor ClusterGroups is set, then the group is on all clusters.
type Target struct {
	// RenameTo is the new name of the group. If not set, the original name will be used.
	RenameTo string `json:"rename_to,omitempty" yaml:"rename_to,omitempty"`
	// Clusters is the clusters where the group should exist.
	Clusters []string `json:"clusters,omitempty" yaml:"clusters,omitempty"`
	// ClusterGroups is the cluster groups where the group should exist.
	ClusterGroups []string `json:"cluster_groups,omitempty" yaml:"cluster_groups,omitempty"`
	// SecretCollections are the secret collections the group has access to.
	SecretCollections []string `json:"secret_collections,omitempty" yaml:"secret_collections,omitempty"`
}

func (t Target) ResolveClusters(cg map[string][]string) sets.Set[string] {
	ret := sets.New[string](t.Clusters...)
	for _, clusterGroup := range t.ClusterGroups {
		ret.Insert(cg[clusterGroup]...)
	}
	return ret
}

// LoadConfig loads the config from a given file
func LoadConfig(file string) (*Config, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to load config file")
	}
	config := &Config{}

	if err := yaml.UnmarshalStrict(data, config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file (strict mode): %w", err)
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("failed to validate config file: %w", err)
	}
	return config, nil
}

// PrintConfig deserializes and re-serializes the config. Removing spaces and comments, and sorting the groups in the process prior to printing to standard out
func PrintConfig(c *Config) error {
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	fmt.Printf("%s", rawYaml)
	return nil
}

func (c *Config) validate() error {
	for k, v := range c.Groups {
		if k == OpenshiftPrivAdminsGroup || v.RenameTo == OpenshiftPrivAdminsGroup {
			return fmt.Errorf("cannot use the group name %s in the configuration file", OpenshiftPrivAdminsGroup)
		}
		for _, collection := range v.SecretCollections {
			if !validation.ValidateCollectionName(collection) {
				return fmt.Errorf("invalid collection name '%s' in the configuration file: must be at most %d characters, contain only lowercase letters, numbers, hyphens, and underscores (no double underscores), and end with a lowercase letter or number", collection, validation.MaxCollectionLength)
			}
		}
	}
	return nil
}
