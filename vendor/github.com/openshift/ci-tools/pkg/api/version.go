package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// VersionTransitionOverrides defines explicit "previous" versions for cross-major transitions.
// First entry is the primary previous. If not in map, natural progression (X.Y-1) is used.
var VersionTransitionOverrides = map[string][]string{
	"5.0": {"4.22"},
	// "5.1": {"5.0", "4.23"},
}

type ParsedVersion struct {
	Major int
	Minor int
}

func (v ParsedVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func ParseVersion(version string) (ParsedVersion, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 2 {
		return ParsedVersion{}, fmt.Errorf("invalid version format: %s (expected X.Y)", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return ParsedVersion{}, fmt.Errorf("invalid major version in %s: %w", version, err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return ParsedVersion{}, fmt.Errorf("invalid minor version in %s: %w", version, err)
	}

	return ParsedVersion{Major: major, Minor: minor}, nil
}

// IsValidOCPVersion validates that the version is in format X.Y where X >= 4.
func IsValidOCPVersion(version string) bool {
	parsed, err := ParseVersion(version)
	if err != nil {
		return false
	}
	return parsed.Major >= 4
}

// GetPreviousVersion returns the primary previous version. For X.0 without override,
// it finds the highest (X-1).* from availableVersions.
func GetPreviousVersion(current string, availableVersions []string) (string, error) {
	if overrides, ok := VersionTransitionOverrides[current]; ok && len(overrides) > 0 {
		return overrides[0], nil
	}

	parsed, err := ParseVersion(current)
	if err != nil {
		return "", err
	}

	if parsed.Minor > 0 {
		return fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor-1), nil
	}

	if parsed.Major <= 0 {
		return "", fmt.Errorf("cannot determine previous version for %s: no previous major version exists", current)
	}

	previousMajor := parsed.Major - 1
	var candidates []ParsedVersion

	for _, v := range availableVersions {
		pv, err := ParseVersion(v)
		if err != nil {
			continue
		}
		if pv.Major == previousMajor {
			candidates = append(candidates, pv)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("cannot determine previous version for %s: no %d.x versions found in available versions", current, previousMajor)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Minor > candidates[j].Minor
	})

	return candidates[0].String(), nil
}

// GetPreviousVersionSimple returns the primary previous version without availableVersions list.
// For X.0 without override, returns error.
func GetPreviousVersionSimple(current string) (string, error) {
	if overrides, ok := VersionTransitionOverrides[current]; ok && len(overrides) > 0 {
		return overrides[0], nil
	}

	parsed, err := ParseVersion(current)
	if err != nil {
		return "", err
	}

	if parsed.Minor > 0 {
		return fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor-1), nil
	}

	return "", fmt.Errorf("cannot determine previous version for %s: no override defined", current)
}
