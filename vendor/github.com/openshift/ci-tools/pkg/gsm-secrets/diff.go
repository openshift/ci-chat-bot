package gsmsecrets

import (
	"slices"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/sirupsen/logrus"
)

func ComputeDiff(
	config Config,
	desiredSAs []ServiceAccountInfo,
	actualSAs []ServiceAccountInfo,
	desiredSecrets map[string]GCPSecret,
	actualSecrets map[string]GCPSecret,
	desiredIAMBindings []*iampb.Binding,
	actualIAMPolicy *iampb.Policy,
	desiredCollections map[string]bool,
) Actions {
	actions := Actions{
		Config: config,
	}

	actions.SAsToCreate, actions.SAsToDelete = DiffServiceAccounts(desiredSAs, actualSAs)
	actions.SecretsToCreate, actions.SecretsToDelete = DiffSecrets(desiredSecrets, actualSecrets, desiredCollections)
	actions.ConsolidatedIAMPolicy = DiffIAMBindings(desiredIAMBindings, actualIAMPolicy)

	return actions
}

func DiffServiceAccounts(desiredSAs []ServiceAccountInfo, actualSAs []ServiceAccountInfo) (toCreate SAMap, toDelete SAMap) {
	desiredSAsMap := make(SAMap)
	for _, sa := range desiredSAs {
		desiredSAsMap[sa.Email] = sa
	}
	actualSAsMap := make(SAMap)
	for _, sa := range actualSAs {
		actualSAsMap[sa.Email] = sa
	}

	toCreate = make(SAMap)
	for _, desiredSA := range desiredSAs {
		if _, found := actualSAsMap[desiredSA.Email]; !found {
			toCreate[desiredSA.Email] = desiredSA
		}
	}

	toDelete = make(SAMap)
	for _, actualSA := range actualSAs {
		if _, found := desiredSAsMap[actualSA.Email]; !found {
			toDelete[actualSA.Email] = actualSA
		}
	}

	return toCreate, toDelete
}

func DiffSecrets(desiredSecrets, actualSecrets map[string]GCPSecret, desiredCollections map[string]bool) (map[string]GCPSecret, []GCPSecret) {
	toCreate := make(map[string]GCPSecret)
	toDelete := make([]GCPSecret, 0)

	for _, secret := range desiredSecrets {
		if _, found := actualSecrets[secret.Name]; !found {
			logrus.Debugf("Scheduling secret '%s' for creation", secret.Name)
			toCreate[secret.Name] = secret
		}
	}

	for _, secret := range actualSecrets {
		if desiredCollections[secret.Collection] {
			continue
		}

		toDelete = append(toDelete, secret)
		logrus.Debugf("Scheduling secret '%s' for deletion (collection '%s' not in config)", secret.Name, secret.Collection)
	}
	slices.SortFunc(toDelete, func(a, b GCPSecret) int {
		return strings.Compare(a.Name, b.Name)
	})
	return toCreate, toDelete
}

func DiffIAMBindings(desiredBindings []*iampb.Binding, actualPolicy *iampb.Policy) *iampb.Policy {
	desiredBindingsMap := make(map[string]*iampb.Binding)
	for _, binding := range desiredBindings {
		key := ToCanonicalIAMBinding(binding).makeCanonicalKey()
		desiredBindingsMap[key] = binding
	}

	actualBindingsMap := make(map[string]*iampb.Binding)
	var unmanagedBindings []*iampb.Binding
	for _, IAMbinding := range actualPolicy.Bindings {
		key := ToCanonicalIAMBinding(IAMbinding).makeCanonicalKey()
		actualBindingsMap[key] = IAMbinding

		if !IsManagedBinding(IAMbinding) {
			unmanagedBindings = append(unmanagedBindings, IAMbinding)
		}
	}

	hasChanges := false
	var finalBindings []*iampb.Binding

	for _, desiredBinding := range desiredBindings {
		key := ToCanonicalIAMBinding(desiredBinding).makeCanonicalKey()
		if _, exists := actualBindingsMap[key]; !exists {
			hasChanges = true
		}
		finalBindings = append(finalBindings, desiredBinding)
	}

	for _, actualBinding := range actualPolicy.Bindings {
		actualKey := ToCanonicalIAMBinding(actualBinding).makeCanonicalKey()
		if _, foundInDesired := desiredBindingsMap[actualKey]; !foundInDesired && IsManagedBinding(actualBinding) {
			logrus.Debugf("Removing obsolete IAM binding: Role=%s, Members=%s, Condition=%s", actualBinding.Role, actualBinding.Members, actualBinding.Condition.GetTitle())
			hasChanges = true
		}
	}

	finalBindings = append(finalBindings, unmanagedBindings...)

	if !hasChanges {
		return nil
	}

	consolidatedPolicy := &iampb.Policy{
		Bindings:     finalBindings,
		Etag:         actualPolicy.Etag,
		Version:      3, // required for IAM conditions support
		AuditConfigs: actualPolicy.AuditConfigs,
	}
	return consolidatedPolicy
}
