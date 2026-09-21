package api

import (
	"strconv"
	"strings"
)

// LeasesForTest aggregates all the lease configurations in a test.
// It is assumed that they have been validated and contain only valid and
// unique values.
func LeasesForTest(test *TestStepConfiguration) (ret []StepLease) {
	multiStageTest := test.MultiStageTestConfigurationLiteral
	if p := multiStageTest.ClusterProfileLiteral; p != nil {
		ret = append(ret, StepLease{
			ResourceType:   p.LeaseType,
			Env:            DefaultLeaseEnv,
			Count:          1,
			ClusterProfile: p,
		})
	}
	for _, step := range append(multiStageTest.Pre, append(multiStageTest.Test, multiStageTest.Post...)...) {
		ret = append(ret, step.Leases...)
	}
	ret = append(ret, multiStageTest.Leases...)
	return
}

const maxAddressesRequired = 13

func IPPoolLeaseForTest(ipPoolLeaseType, branch string) StepLease {
	if ipPoolLeaseType != "" && branchValidForIPPoolLease(branch) {
		return StepLease{
			ResourceType: ipPoolLeaseType,
			Env:          DefaultIPPoolLeaseEnv,
			Count:        maxAddressesRequired,
		}
	}
	return StepLease{}
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
