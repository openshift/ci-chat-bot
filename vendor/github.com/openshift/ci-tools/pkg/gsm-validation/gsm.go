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
	// UnderscorePrefix/Suffix for encoding consecutive underscores
	// __ → --uu--, ___ → --uuu--, etc.
	UnderscorePrefix = "--"
	UnderscoreSuffix = "--"

	CollectionRegex = "^([a-z0-9_-]*[a-z0-9])?$"
	GroupRegex      = `^(--dot--)?[a-zA-Z0-9]+([a-zA-Z0-9_-]*[a-zA-Z0-9]+)?(/[a-zA-Z0-9]+([a-zA-Z0-9_-]*[a-zA-Z0-9]+)?)*$`
	SecretNameRegex = "^[A-Za-z0-9_-]+$"

	// MaxCollectionLength is the maximum length of a collection name
	MaxCollectionLength = 50

	// GcpMaxNameLength is the maximum length for a GSM secret name
	GcpMaxNameLength = 255
)

var (
	// Regex to find consecutive underscores (2 or more)
	consecutiveUnderscoresRegex = regexp.MustCompile(`_{2,}`)
	// Regex to find encoded underscores in format --uu-- (2 or more u's)
	encodedUnderscoresRegex = regexp.MustCompile(`--u{2,}--`)
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

	// Cannot start or end with underscore
	if strings.HasPrefix(collection, "_") {
		return false
	}
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

	// Cannot start or end with underscore
	if strings.HasPrefix(secretName, "_") {
		return false
	}
	if strings.HasSuffix(secretName, "_") {
		return false
	}

	// Cannot contain double underscore (conflicts with delimiter)
	if strings.Contains(secretName, CollectionSecretDelimiter) {
		return false
	}
	return secretNameRegexp.MatchString(secretName)
}

// NormalizeName replaces forbidden characters in names with safe replacements.
// This is used when migrating from Vault to GSM to handle special characters.
// Rules (applied in order):
//  1. `__` (2+ consecutive underscores) → `--uu--`, `--uuu--`, etc.
//  2. `.` → `--dot--` (dots not allowed in GSM secret names)
//
// Examples:
//   - ".dockerconfigjson" → "--dot--dockerconfigjson"
//   - "mac_ai__base_dir" → "mac_ai--uu--base_dir"
//   - "some___field" → "some--uuu--field"
//   - "field.with__both" → "field--dot--with--uu--both"
func NormalizeName(name string) string {
	result := consecutiveUnderscoresRegex.ReplaceAllStringFunc(name, func(match string) string {
		count := len(match)
		return UnderscorePrefix + strings.Repeat("u", count) + UnderscoreSuffix
	})
	return strings.ReplaceAll(result, ".", DotReplacementString)
}

// DenormalizeName decodes field names back to their original form.
// This reverses the encoding done by NormalizeName.
// Decodes in reverse order of NormalizeName.
func DenormalizeName(name string) string {
	result := strings.ReplaceAll(name, DotReplacementString, ".")
	result = encodedUnderscoresRegex.ReplaceAllStringFunc(result, func(match string) string {
		uCount := len(match) - len(UnderscorePrefix) - len(UnderscoreSuffix)
		return strings.Repeat("_", uCount)
	})
	return result
}
