package gsmvalidation

import (
	"regexp"
	"strings"
)

const (
	// CollectionSecretDelimiter is the separator between collection and secret name in GSM
	CollectionSecretDelimiter = "__"

	// Encoding constants for special characters
	DotReplacementString = "--dot--"

	CollectionRegex = "^([a-z0-9_-]*[a-z0-9])?$"
	GroupRegex      = `^[a-z0-9]+([a-z0-9_-]*[a-z0-9]+)?(/[a-z0-9]+([a-z0-9_-]*[a-z0-9]+)?)*$`
	SecretNameRegex = "^[A-Za-z0-9_-]+$"

	// MaxCollectionLength is the maximum length of a collection name
	MaxCollectionLength = 50

	// GcpMaxNameLength is the maximum length for a GSM secret name
	GcpMaxNameLength = 255
)

var (
	collectionRegexp = regexp.MustCompile(CollectionRegex)
	groupRegexp      = regexp.MustCompile(GroupRegex)
	secretNameRegexp = regexp.MustCompile(SecretNameRegex)
)

// ValidateCollectionName validates a GSM collection name
func ValidateCollectionName(collection string) bool {
	if collection == "" || len(collection) > MaxCollectionLength {
		return false
	}

	// Cannot end with underscore (would create collection___secret)
	if strings.HasSuffix(collection, "_") {
		return false
	}

	// Cannot contain double underscore (conflicts with delimiter)
	if strings.Contains(collection, CollectionSecretDelimiter) {
		return false
	}

	return collectionRegexp.MatchString(collection)
}

func ValidateGroupName(group string) bool {
	if group == "" {
		return false
	}

	if strings.HasPrefix(group, "_") {
		return false
	}

	if strings.HasSuffix(group, "_") {
		return false
	}

	if strings.Contains(group, "__") {
		return false
	}

	return groupRegexp.MatchString(group)
}

// ValidateSecretName validates a GSM secret name
func ValidateSecretName(secretName string) bool {
	if secretName == "" || len(secretName) > GcpMaxNameLength {
		return false
	}

	// Cannot start with underscore (would create collection___secret)
	if strings.HasPrefix(secretName, "_") {
		return false
	}

	// Cannot contain double underscore (conflicts with delimiter)
	if strings.Contains(secretName, CollectionSecretDelimiter) {
		return false
	}
	return secretNameRegexp.MatchString(secretName)
}

// NormalizeName replaces forbidden characters in field names with safe replacements.
// This is used when migrating from Vault to GSM to handle special characters in field names.
// Rules:
//   - `.` → `--dot--` (dots not allowed in GSM secret names)
//
// Example: ".dockerconfigjson" → "--dot--dockerconfigjson"
func NormalizeName(name string) string {
	// Encode in specific order to avoid conflicts
	return strings.ReplaceAll(name, ".", DotReplacementString)
}

// DenormalizeName decodes field names back to their original form.
// This reverses the encoding done by NormalizeName.
func DenormalizeName(name string) string {
	// Decode in reverse order
	return strings.ReplaceAll(name, DotReplacementString, ".")
}
