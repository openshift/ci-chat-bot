package api

import (
	"strconv"
	"strings"
)

// LeasesForTest aggregates all the lease configurations in a test.
// It is assumed that they have been validated and contain only valid and
// unique values.
func LeasesForTest(s *MultiStageTestConfigurationLiteral) (ret []StepLease) {
	if p := s.ClusterProfile; p != "" {
		ret = append(ret, StepLease{
			ResourceType: p.LeaseType(),
			Env:          DefaultLeaseEnv,
			Count:        1,
		})
	}
	for _, step := range append(s.Pre, append(s.Test, s.Post...)...) {
		ret = append(ret, step.Leases...)
	}
	ret = append(ret, s.Leases...)
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
	openshiftBranch = "openshift-4."
	releaseBranch   = "release-4."
	minimumVersion  = 16
)

// Currently, we only have the ability to utilize IP pools in 4.16 and later, we want to make sure not to allocate them
// on earlier versions
func branchValidForIPPoolLease(branch string) bool {
	if branch == "master" || branch == "main" {
		return true
	}
	var version string
	if strings.HasPrefix(branch, openshiftBranch) {
		version = strings.TrimPrefix(branch, openshiftBranch)
	}
	if strings.HasPrefix(branch, releaseBranch) {
		version = strings.TrimPrefix(branch, releaseBranch)
	}
	number, err := strconv.Atoi(version)
	if err != nil {
		return false
	}

	return number >= minimumVersion
}
