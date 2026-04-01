package gsmsecrets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/sirupsen/logrus"
)

// BuildSecretAccessorRoleConditionExpression builds the IAM condition expression for secret accessor role
func BuildSecretAccessorRoleConditionExpression(collection string) string {
	// Define the two specific secrets this role can access
	updaterSecret := fmt.Sprintf("%s%s", collection, UpdaterSASecretSuffix)
	indexSecret := fmt.Sprintf("%s%s", collection, IndexSecretSuffix)

	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && (
  resource.name.extract("secrets/{secret}") == "%s" ||
  resource.name.extract("secrets/{secret}") == "%s"
)`, updaterSecret, indexSecret)
}

// BuildSecretUpdaterRoleConditionExpression builds the IAM condition expression for secret updater role
func BuildSecretUpdaterRoleConditionExpression(collection string) string {
	return fmt.Sprintf(`(
  resource.type == "secretmanager.googleapis.com/SecretVersion" ||
  resource.type == "secretmanager.googleapis.com/Secret"
) && 
  resource.name.extract("secrets/{secret}").startsWith("%s__")`, collection)
}

// GetSecretsViewerConditionTitle returns the condition title for secrets viewer role
func GetSecretsViewerConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsViewerConditionTitlePrefix, collection)
}

// GetSecretsUpdaterConditionTitle returns the condition title for secrets updater role
func GetSecretsUpdaterConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsUpdaterConditionTitlePrefix, collection)
}

// GetSecretsViewerConditionDescription returns the condition description for secrets viewer role
func GetSecretsViewerConditionDescription(collection string) string {
	return fmt.Sprintf(SecretsViewerConditionDescriptionTemplate, TestPlatform, collection)
}

// GetSecretsUpdaterConditionDescription returns the condition description for secrets updater role
func GetSecretsUpdaterConditionDescription(collection string) string {
	return fmt.Sprintf(SecretsUpdaterConditionDescriptionTemplate, TestPlatform, collection)
}

// IsManagedBinding checks if an IAM binding is managed by this tool.
func IsManagedBinding(b *iampb.Binding) bool {
	isSecretAccessorRole := strings.Contains(b.Role, "/roles/openshift_ci_secrets_viewer")
	isSecretUpdaterRole := strings.Contains(b.Role, "/roles/openshift_ci_secrets_updater")
	if !(isSecretAccessorRole || isSecretUpdaterRole) {
		return false
	}
	if b.Condition == nil {
		return false
	}

	title := b.Condition.GetTitle()
	description := b.Condition.GetDescription()

	titleMatches := strings.HasPrefix(title, SecretsViewerConditionTitlePrefix) ||
		strings.HasPrefix(title, SecretsUpdaterConditionTitlePrefix)
	descriptionMatches := strings.HasPrefix(description, fmt.Sprintf("Managed by %s:", TestPlatform))

	if !titleMatches || !descriptionMatches {
		return false
	}

	expr := b.Condition.Expression
	hasSecretManagerResource := strings.Contains(expr, "secretmanager.googleapis.com")
	hasSecretExtract := strings.Contains(expr, `resource.name.extract("secrets/{secret}")`)
	hasExpectedPattern := strings.Contains(expr, "startsWith(") || strings.Contains(expr, "==")

	return hasSecretManagerResource && hasSecretExtract && hasExpectedPattern
}

// ToCanonicalIAMBinding converts an iampb.Binding into our canonical form.
// This is necessary for consistent key generation and comparison.
func ToCanonicalIAMBinding(b *iampb.Binding) CanonicalIAMBinding {
	members := make([]string, len(b.Members))
	copy(members, b.Members)
	sort.Strings(members)

	conditionExpr := ""
	conditionTitle := ""
	conditionDesc := ""
	if b.Condition != nil {
		conditionExpr = b.Condition.Expression
		conditionTitle = b.Condition.GetTitle()
		conditionDesc = b.Condition.GetDescription()
	}

	return CanonicalIAMBinding{
		Role:           b.Role,
		Members:        strings.Join(members, ","),
		ConditionExpr:  conditionExpr,
		ConditionTitle: conditionTitle,
		ConditionDesc:  conditionDesc,
	}
}

// makeCanonicalKey generates a canonical key for IAM binding comparison
func (c CanonicalIAMBinding) makeCanonicalKey() string {
	jsonData, err := json.Marshal(c)
	if err != nil {
		logrus.Fatal(err)
	}
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:])
}
