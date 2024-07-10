package api

import "strings"

// IsCreatedForClusterBotJob returns true if the given namespace is created for a job from the cluster bot
func IsCreatedForClusterBotJob(namespace string) bool {
	return strings.HasPrefix(namespace, "ci-ln-")
}
