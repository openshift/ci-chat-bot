package gsmsecrets

import (
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	validation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// ClassifySecret determines the type of secret based on its name
func ClassifySecret(secretName string) SecretType {
	if strings.HasSuffix(secretName, UpdaterSASecretSuffix) {
		return SecretTypeSA
	}
	if strings.HasSuffix(secretName, IndexSecretSuffix) {
		return SecretTypeIndex
	}
	if strings.Contains(secretName, "__") {
		return SecretTypeGeneric
	}
	return SecretTypeUnknown
}

// ExtractCollectionFromSecretName returns the substring before the first "__" in a secret name.
// Supports both 2-level (collection__field) and 3-level (collection__group__field) hierarchies.
func ExtractCollectionFromSecretName(secretName string) string {
	// Special case: index secrets (collection____index)
	if strings.HasSuffix(secretName, IndexSecretSuffix) {
		collection := strings.TrimSuffix(secretName, IndexSecretSuffix)
		if collection != "" && validation.ValidateCollectionName(collection) {
			return collection
		}
		return ""
	}

	// Reject malformed index secrets (contains ____index but doesn't end with it)
	if strings.Contains(secretName, IndexSecretSuffix) {
		return ""
	}

	// Split by delimiter
	parts := strings.Split(secretName, "__")

	// Need at least 2 parts: collection and field (or collection, group, field, etc.)
	if len(parts) < 2 {
		return ""
	}

	// Validate collection (first part)
	collection := parts[0]
	if !validation.ValidateCollectionName(collection) {
		return ""
	}

	// Validate that we have at least one more non-empty part
	hasValidPart := false
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			hasValidPart = true
			break
		}
	}

	if !hasValidPart {
		return ""
	}

	return collection
}

// EmptyIndexSecretContent is the index content for a collection holding no secrets.
const EmptyIndexSecretContent = "[]"

// VerifyIndexSecretContent verifies that a fresh collection's index secret is empty.
func VerifyIndexSecretContent(payload []byte) error {
	actualContent := strings.TrimSpace(string(payload))

	if actualContent != EmptyIndexSecretContent {
		return fmt.Errorf("index secret content mismatch: expected %q, got %q", EmptyIndexSecretContent, actualContent)
	}

	return nil
}

// ConstructIndexSecretContent constructs the index secret content from the secretsList.
// The updater service account secret is not listed: it is reached with the CLI's get-sa, and
// listing it would go stale, since index content is only written when the index is created.
func ConstructIndexSecretContent(secretsList []string) []byte {
	if len(secretsList) == 0 {
		return []byte(EmptyIndexSecretContent)
	}
	sorted := make([]string, len(secretsList))
	copy(sorted, secretsList)
	sort.Strings(sorted)

	var formattedSecrets []string
	for _, secret := range sorted {
		formattedSecrets = append(formattedSecrets, fmt.Sprintf("- %s", secret))
	}

	return []byte(strings.Join(formattedSecrets, "\n"))
}

// ParseIndexSecretContent parses the index secret YAML content and returns the list of secret
// names. UpdaterSASecretName is filtered out for indexes created before it stopped being written.
func ParseIndexSecretContent(content []byte) []string {
	var allSecrets []string
	if err := yaml.Unmarshal(content, &allSecrets); err != nil {
		return []string{}
	}

	var secrets []string
	for _, secret := range allSecrets {
		if secret != UpdaterSASecretName {
			secrets = append(secrets, secret)
		}
	}
	return secrets
}
