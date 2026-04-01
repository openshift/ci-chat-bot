package gsmsecrets

import (
	"fmt"
	"sort"

	"cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/genproto/googleapis/type/expr"

	"github.com/openshift/ci-tools/pkg/group"
)

// GetDesiredState parses the configuration file and builds the desired state specifications.
// For each unique secret collection referenced by groups, it generates the required resource definitions.
// Returns desired service account specs, secret specs, IAM binding specs, and the set of active collections.
func GetDesiredState(configFile string, config Config) ([]ServiceAccountInfo, map[string]GCPSecret, []*iampb.Binding, map[string]bool, error) {
	groupConfig, err := group.LoadConfig(configFile)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to load file: %w", err)
	}
	collectionsMap := make(map[string]DesiredCollection)

	for name, groupCfg := range groupConfig.Groups {
		email := fmt.Sprintf("%s@redhat.com", name)

		for _, collection := range groupCfg.SecretCollections {
			if _, found := collectionsMap[collection]; !found {
				collectionsMap[collection] = DesiredCollection{
					Name:             collection,
					GroupsWithAccess: []string{email},
				}
			} else {
				col := collectionsMap[collection]
				col.GroupsWithAccess = append(col.GroupsWithAccess, email)
				collectionsMap[collection] = col
			}
		}
	}

	var desiredSAs []ServiceAccountInfo
	desiredSecrets := make(map[string]GCPSecret)
	desiredCollections := make(map[string]bool)
	var desiredIAMBindings []*iampb.Binding

	var collectionNames []string
	for name := range collectionsMap {
		collectionNames = append(collectionNames, name)
	}
	sort.Strings(collectionNames)

	for _, collectionName := range collectionNames {
		collection := collectionsMap[collectionName]
		desiredCollections[collection.Name] = true

		desiredSAs = append(desiredSAs, ServiceAccountInfo{
			Email:       GetUpdaterSAEmail(collection.Name, config),
			DisplayName: GetUpdaterSADisplayName(collection.Name),
			ID:          GetUpdaterSAId(collection.Name),
			Collection:  collection.Name,
			Description: GetUpdaterSADescription(collection.Name),
		})

		desiredSecrets[GetUpdaterSASecretName(collection.Name)] = GCPSecret{
			Name:       GetUpdaterSASecretName(collection.Name),
			Type:       SecretTypeSA,
			Collection: collection.Name,
		}

		desiredSecrets[GetIndexSecretName(collection.Name)] = GCPSecret{
			Name:       GetIndexSecretName(collection.Name),
			Type:       SecretTypeIndex,
			Collection: collection.Name,
		}

		var members []string
		for _, groupWithAccess := range collection.GroupsWithAccess {
			members = append(members, fmt.Sprintf("group:%s", groupWithAccess))
		}
		members = append(members, fmt.Sprintf("serviceAccount:%s", GetUpdaterSAEmail(collection.Name, config)))
		sort.Strings(members)

		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretAccessorRole(),
			Members: members,
			Condition: &expr.Expr{
				Expression:  BuildSecretAccessorRoleConditionExpression(collection.Name),
				Title:       GetSecretsViewerConditionTitle(collection.Name),
				Description: GetSecretsViewerConditionDescription(collection.Name),
			},
		})
		desiredIAMBindings = append(desiredIAMBindings, &iampb.Binding{
			Role:    config.GetSecretUpdaterRole(),
			Members: members,
			Condition: &expr.Expr{
				Expression:  BuildSecretUpdaterRoleConditionExpression(collection.Name),
				Title:       GetSecretsUpdaterConditionTitle(collection.Name),
				Description: GetSecretsUpdaterConditionDescription(collection.Name),
			},
		})
	}

	return desiredSAs, desiredSecrets, desiredIAMBindings, desiredCollections, nil
}
