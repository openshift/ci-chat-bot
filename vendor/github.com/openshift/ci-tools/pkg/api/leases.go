package api

import (
	"fmt"
	"strconv"
	"strings"
)

// LeasesForTest aggregates all the lease configurations in a test.
// It is assumed that they have been validated and contain only valid and
// unique values.
func LeasesForTest(test *TestStepConfiguration, targetAdditionalSuffix string) (ret []StepLease) {
	multiStageTest := test.MultiStageTestConfigurationLiteral
	if p := multiStageTest.ClusterProfile; p != "" {
		clusterProfileTarget := test.As
		if targetAdditionalSuffix != "" {
			clusterProfileTarget = strings.TrimSuffix(clusterProfileTarget, fmt.Sprintf("-%s", targetAdditionalSuffix))
		}
		ret = append(ret, StepLease{
			ResourceType:         p.LeaseType(),
			Env:                  DefaultLeaseEnv,
			Count:                1,
			ClusterProfile:       multiStageTest.ClusterProfile.Name(),
			ClusterProfileTarget: clusterProfileTarget,
		})
	}
	for _, step := range append(multiStageTest.Pre, append(multiStageTest.Test, multiStageTest.Post...)...) {
		ret = append(ret, step.Leases...)
	}
	ret = append(ret, multiStageTest.Leases...)
	return
}

const maxAddressesRequired = 13

func IPPoolLeaseForTest(s *MultiStageTestConfigurationLiteral, metadata Metadata) (ret StepLease) {
	p := s.ClusterProfile
	if p != "" {
		if lt := p.IPPoolLeaseType(); lt != "" {
			if !p.IPPoolLeaseShouldValidateBranch() || branchValidForIPPoolLease(metadata.Branch) {
				ret = StepLease{
					ResourceType: lt,
					Env:          DefaultIPPoolLeaseEnv,
					Count:        maxAddressesRequired,
				}
			}
		}
	}
	return
}

const (
	// minimumIPPoolMajorVersion and minimumIPPoolMinorVersion define the minimum OCP version
	// that supports IP pools (4.16+)
	minimumIPPoolMajorVersion = 4
	minimumIPPoolMinorVersion = 16
)

// Currently, we only have the ability to utilize IP pools in 4.16 and later, we want to make sure not to allocate them
// on earlier versions
func branchValidForIPPoolLease(branch string) bool {
	if branch == "master" || branch == "main" {
		return true
	}

	major, minor, ok := parseVersionFromBranch(branch)
	if !ok {
		return false
	}

	// Version 5.x and later is always valid
	if major > minimumIPPoolMajorVersion {
		return true
	}
	// For version 4.x, check if minor version meets minimum requirement
	return major == minimumIPPoolMajorVersion && minor >= minimumIPPoolMinorVersion
}

// parseVersionFromBranch extracts major and minor version from branch names like
// "openshift-4.16", "release-5.0", etc.
func parseVersionFromBranch(branch string) (major, minor int, ok bool) {
	prefixes := []string{"openshift-", "release-"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(branch, prefix) {
			versionStr := strings.TrimPrefix(branch, prefix)
			parts := strings.Split(versionStr, ".")
			if len(parts) != 2 {
				continue
			}
			majorNum, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			minorNum, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			return majorNum, minorNum, true
		}
	}
	return 0, 0, false
}
