package gsmsecrets

import (
	"slices"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
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
		if !desiredCollections[secret.Collection] {
			toDelete = append(toDelete, secret)
			logrus.Debugf("Scheduling secret '%s' for deletion (collection '%s' not in config)", secret.Name, secret.Collection)
			continue
		}

		if _, wanted := desiredSecrets[secret.Name]; !wanted && secret.Type == SecretTypeSA {
			toDelete = append(toDelete, secret)
			logrus.Debugf("Scheduling secret '%s' for deletion (collection '%s' has no updater service account)", secret.Name, secret.Collection)
		}
	}
	slices.SortFunc(toDelete, func(a, b GCPSecret) int {
		return strings.Compare(a.Name, b.Name)
	})
	return toCreate, toDelete
}

// MergeBindingsByCondition unions the members of bindings sharing a role and condition.
// GCP stores such bindings merged, so the desired state must be expressed the same way or it
// never compares equal to the policy GCP reports back.
func MergeBindingsByCondition(bindings []*iampb.Binding) []*iampb.Binding {
	type conditionKey struct {
		role, title, expression string
	}

	var order []conditionKey
	members := map[conditionKey]sets.Set[string]{}
	first := map[conditionKey]*iampb.Binding{}

	for _, binding := range bindings {
		key := conditionKey{binding.Role, binding.Condition.GetTitle(), binding.Condition.GetExpression()}
		if _, seen := first[key]; !seen {
			members[key] = sets.New[string]()
			first[key] = binding
			order = append(order, key)
		}
		members[key].Insert(binding.Members...)
	}

	result := make([]*iampb.Binding, 0, len(order))
	for _, key := range order {
		result = append(result, &iampb.Binding{
			Role:      first[key].Role,
			Members:   sets.List(members[key]),
			Condition: first[key].Condition,
		})
	}
	return result
}

func DiffIAMBindings(desiredBindings []*iampb.Binding, actualPolicy *iampb.Policy) *iampb.Policy {
	desiredBindings = MergeBindingsByCondition(desiredBindings)

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
