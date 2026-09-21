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

// The two roles need different templates because GCP checks their permissions on different
// resources. The viewer only holds versions.access, checked on "projects/P/secrets/S/versions/V",
// so anchoring on "/versions/" makes {s} stop at the secret name. The updater also holds
// secrets.get/update/delete, checked on the bare "projects/P/secrets/S", where that anchor finds
// nothing and returns ""; anchoring on the "__" delimiter works on both resource types, since
// extract stops at the first "__" and so yields the bare collection name even for "<c>____index"
// and nested "<c>__group__field".
//
// Matching with "in" rather than chained "==" or startsWith() is what lets one binding cover a
// whole group: "in" does not count against GCP's limit of 12 logical operators per condition.
//
// Dropping the "resource.type" guard is safe: an unmatched template returns "" and the
// condition is false, so access fails closed.
const (
	viewerSecretNameTemplate  = `resource.name.extract("secrets/{s}/versions/")`
	updaterSecretNameTemplate = `resource.name.extract("secrets/{c}__")`
)

// BuildSecretAccessorRoleConditionExpression builds the IAM condition expression for secret accessor role
func BuildSecretAccessorRoleConditionExpression(collection string) string {
	return BuildSecretAccessorRoleConditionExpressionForCollections([]string{collection})
}

// BuildSecretUpdaterRoleConditionExpression builds the IAM condition expression for secret updater role
func BuildSecretUpdaterRoleConditionExpression(collection string) string {
	return BuildSecretUpdaterRoleConditionExpressionForCollections([]string{collection})
}

// BuildSecretAccessorRoleConditionExpressionForCollections builds the viewer IAM condition
// expression covering multiple collections: each collection's updater service account secret
// and its index secret, never its data secrets.
func BuildSecretAccessorRoleConditionExpressionForCollections(collections []string) string {
	var names []string
	for _, collection := range collections {
		names = append(names,
			fmt.Sprintf("%s%s", collection, UpdaterSASecretSuffix),
			fmt.Sprintf("%s%s", collection, IndexSecretSuffix),
		)
	}
	return fmt.Sprintf("%s in [%s]", viewerSecretNameTemplate, quoteJoin(names))
}

// BuildSecretUpdaterRoleConditionExpressionForCollections builds the updater IAM condition
// expression covering multiple collections: any secret in any of them.
func BuildSecretUpdaterRoleConditionExpressionForCollections(collections []string) string {
	return fmt.Sprintf("%s in [%s]", updaterSecretNameTemplate, quoteJoin(collections))
}

// quoteJoin renders values as a comma-separated list of CEL string literals. Collection and
// secret names are restricted to [a-z0-9_-], so no escaping is needed.
func quoteJoin(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return strings.Join(quoted, ", ")
}

// GetSecretsViewerConditionTitle returns the condition title for secrets viewer role
func GetSecretsViewerConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsViewerConditionTitlePrefix, collection)
}

// GetSecretsViewerGroupConditionTitle returns the viewer condition title for a group binding.
func GetSecretsViewerGroupConditionTitle(group string) string {
	return fmt.Sprintf("%s%s", SecretsViewerConditionTitlePrefix, group)
}

// GetSecretsUpdaterGroupConditionTitle returns the updater condition title for a group binding.
func GetSecretsUpdaterGroupConditionTitle(group string) string {
	return fmt.Sprintf("%s%s", SecretsUpdaterConditionTitlePrefix, group)
}

// GetSecretsUpdaterConditionTitle returns the condition title for secrets updater role
func GetSecretsUpdaterConditionTitle(collection string) string {
	return fmt.Sprintf("%s%s", SecretsUpdaterConditionTitlePrefix, collection)
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

	// The title prefix is what separates our bindings from hand-made ones on the same roles,
	// such as the "EXCEPTION: ..." grants, which must be left untouched.
	title := b.Condition.GetTitle()
	if !strings.HasPrefix(title, SecretsViewerConditionTitlePrefix) &&
		!strings.HasPrefix(title, SecretsUpdaterConditionTitlePrefix) {
		return false
	}

	expr := b.Condition.Expression
	hasSecretExtract := strings.Contains(expr, "resource.name.extract(")
	hasExpectedPattern := strings.Contains(expr, " in [") ||
		strings.Contains(expr, "startsWith(") ||
		strings.Contains(expr, "==")

	return hasSecretExtract && hasExpectedPattern
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
