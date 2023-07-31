package api

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	routev1 "github.com/openshift/api/route/v1"
)

const (
	// ServiceDomain is the domain under which services are
	// routed for the current service cluster.
	ServiceDomainCI    = "ci.openshift.org"
	ServiceDomainAPPCI = "apps.ci.l2s4.p1.openshiftapps.com"
	ServiceDomainGCS   = "googleapis.com"

	ServiceDomainAPPCIRegistry   = "registry.ci.openshift.org"
	ServiceDomainVSphereRegistry = "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
	ServiceDomainArm01Registry   = "registry.arm-build01.arm-build.devcluster.openshift.com"
	ServiceDomainMulti01Registry = "registry.multi-build01.arm-build.devcluster.openshift.com"

	QuayOpenShiftCIRepo = "quay.io/openshift/ci"
)

type Service string

const (
	ServiceBoskos     Service = "boskos-ci"
	ServiceRegistry   Service = "registry"
	ServiceRPMs       Service = "artifacts-rpms-openshift-origin-ci-rpms"
	ServiceProw       Service = "prow"
	ServiceConfig     Service = "config"
	ServiceGCSWeb     Service = "gcsweb-ci"
	ServiceGCSStorage Service = "storage"
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
	case ServiceGCSStorage:
		serviceDomain = ServiceDomainGCS
	default:
		serviceDomain = ServiceDomainCI
	}
	return fmt.Sprintf("%s.%s", service, serviceDomain)
}

var (
	buildClusterRegEx = regexp.MustCompile(`build\d\d+`)
)

func RegistryDomainForClusterName(clusterName string) (string, error) {
	if clusterName == string(ClusterAPPCI) {
		return ServiceDomainAPPCIRegistry, nil
	}
	if clusterName == string(ClusterVSphere) {
		return ServiceDomainVSphereRegistry, nil
	}
	if clusterName == string(ClusterARM01) {
		return ServiceDomainArm01Registry, nil
	}
	if clusterName == string(ClusterMulti01) {
		return ServiceDomainMulti01Registry, nil
	}
	if buildClusterRegEx.MatchString(clusterName) {
		return fmt.Sprintf("registry.%s.ci.openshift.org", clusterName), nil
	}
	return "", fmt.Errorf("failed to get the domain for cluster %s", clusterName)
}

// ResolveConsoleHost resolves the console host
func ResolveConsoleHost(ctx context.Context, client ctrlruntimeclient.Client) (string, error) {
	routes := &routev1.RouteList{}
	namespace := "openshift-console"
	if err := client.List(ctx, routes, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("failed to list routes in namespace %s: %w", namespace, err)
	}

	hostForRoute := func(name string, routes []routev1.Route) string {
		for _, route := range routes {
			if route.Name == name {
				return route.Spec.Host
			}
		}
		return ""
	}
	// the canonical route for the console may be in one of two routes,
	// and we want to prefer the custom one if it is present
	for _, routeName := range []string{"console-custom", "console"} {
		if host := hostForRoute(routeName, routes.Items); host != "" {
			return host, nil
		}
	}
	return "", fmt.Errorf("failed to resolve the console host")
}

// ResolveImageRegistryHost resolves the image registry host
func ResolveImageRegistryHost(ctx context.Context, client ctrlruntimeclient.Client) (string, error) {
	routes := &routev1.RouteList{}
	namespace := "openshift-image-registry"
	if err := client.List(ctx, routes, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return "", fmt.Errorf("failed to list routes in namespace %s: %w", namespace, err)
	}

	var someHost, defaultHost, customHost string
	for _, route := range routes.Items {
		if route.Name == "default-route" {
			defaultHost = route.Spec.Host
		}
		if strings.HasSuffix(route.Spec.Host, "ci.openshift.org") {
			customHost = route.Spec.Host
		}
		someHost = route.Spec.Host
	}
	if customHost != "" {
		return customHost, nil
	}
	if someHost != "" {
		return someHost, nil
	}
	if defaultHost != "" {
		return defaultHost, nil
	}

	return "", fmt.Errorf("failed to resolve the image registry host")
}
