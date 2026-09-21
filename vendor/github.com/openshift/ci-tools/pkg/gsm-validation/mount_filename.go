package gsmvalidation

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var mountFileNameCharRegexp = regexp.MustCompile(`[^a-zA-Z0-9\-._]`)

// DecodeMountFileName decodes a GSM-encoded mount filename (field name or `as` alias)
// and validates it is safe to use as a CSI SecretProviderClass file name.
func DecodeMountFileName(encoded string) (string, error) {
	decoded := DenormalizeName(encoded)
	if err := validateDecodedMountFileName(encoded, decoded); err != nil {
		return "", err
	}
	return decoded, nil
}

// ValidateMountFileName validates a GSM-encoded mount filename without returning the decoded value.
func ValidateMountFileName(encoded string) error {
	_, err := DecodeMountFileName(encoded)
	return err
}

func validateDecodedMountFileName(encoded, decoded string) error {
	if decoded == "" {
		return fmt.Errorf("mount file name %q decodes to an empty string", encoded)
	}
	if strings.HasPrefix(decoded, "/") {
		return fmt.Errorf("mount file name %q decodes to %q which is an absolute path", encoded, decoded)
	}
	if slices.Contains(strings.Split(decoded, "/"), "..") {
		return fmt.Errorf("mount file name %q decodes to %q which contains a path traversal segment", encoded, decoded)
	}
	if invalidCharacters := mountFileNameCharRegexp.FindAllString(decoded, -1); len(invalidCharacters) > 0 {
		return fmt.Errorf(
			"mount file name %q decodes to %q which contains forbidden characters (%s); decoded names must only contain letters, numbers, dashes (-), dots (.), and underscores (_)",
			encoded, decoded, strings.Join(invalidCharacters, ", "),
		)
	}
	return nil
}
