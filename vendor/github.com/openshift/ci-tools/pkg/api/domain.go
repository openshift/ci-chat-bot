package api

import (
	"fmt"
	"strings"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomainCI    = "ci.openshift.org"
	ServiceDomainAPPCI = "apps.ci.l2s4.p1.openshiftapps.com"

	ServiceDomainAPPCIRegistry = "registry.ci.openshift.org"
)

type Service string

const (
	ServiceBoskos   Service = "boskos-ci"
	ServiceRegistry Service = "registry"
	ServiceRPMs     Service = "artifacts-rpms-openshift-origin-ci-rpms"
	ServiceProw     Service = "prow"
	ServiceConfig   Service = "config"
	ServiceGCSWeb   Service = "gcsweb-ci"
)

// URLForService returns the URL for the service including scheme
func URLForService(service Service) string {
	return fmt.Sprintf("https://%s", DomainForService(service))
}

// DomainForService returns the DNS domain name for the service
func DomainForService(service Service) string {
	var serviceDomain string
	switch service {
	case ServiceBoskos, ServiceGCSWeb:
		serviceDomain = ServiceDomainAPPCI
	case ServiceRPMs:
		serviceDomain = ServiceDomainAPPCI
	default:
		serviceDomain = ServiceDomainCI
	}
	return fmt.Sprintf("%s.%s", service, serviceDomain)
}

// PublicDomainForImage replaces the registry service DNS name and port with the public domain for the registry for the given cluster
// It will raise an error when the cluster is not recognized
func PublicDomainForImage(ClusterName, potentiallyPrivate string) (string, error) {
	d, err := RegistryDomainForClusterName(ClusterName)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(potentiallyPrivate, "image-registry.openshift-image-registry.svc:5000", d), nil
}

func RegistryDomainForClusterName(ClusterName string) (string, error) {
	switch ClusterName {
	case string(ClusterAPPCI):
		return ServiceDomainAPPCIRegistry, nil
	}
	return "", fmt.Errorf("failed to get the domain for cluster %s", ClusterName)
}
