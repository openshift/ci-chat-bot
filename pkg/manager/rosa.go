package manager

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/openshift/rosa/pkg/interactive"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/openshift/ci-chat-bot/pkg/utils"

	"github.com/openshift/rosa/pkg/arguments"
	"github.com/openshift/rosa/pkg/aws"
	"github.com/openshift/rosa/pkg/helper/roles"
	"github.com/openshift/rosa/pkg/ocm"

	awssdk "github.com/aws/aws-sdk-go/aws"
	clustermgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/oc/pkg/helpers/tokencmd"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"k8s.io/test-infra/prow/metrics"
)

var RosaClusterSecretName = "ci-chat-bot-rosa-clusters"

func (m *jobManager) createRosaCluster(providedVersion, slackID, slackChannel string, duration time.Duration) (*clustermgmtv1.Cluster, string, error) {
	clusterName, err := generateRandomString(15, true)
	if err != nil {
		return nil, "", fmt.Errorf("Failed to generate random name: %v", err)
	}
	versions := m.lookupRosaVersions(providedVersion)
	if len(versions) == 0 {
		return nil, "", fmt.Errorf("No supported openshift version for Rosa with prefix `%s` found.\nSupported versions: %s", providedVersion, m.getSupportedRosaVersions())
	}
	rawVersion := versions[0]
	var version string
	// do this in an inline function to unlock rosaVersions mutex more quickly and ensure it is removed on error as well
	if err := func() error {
		m.rosaVersions.lock.RLock()
		defer m.rosaVersions.lock.RUnlock()
		version, err = m.rClient.OCMClient.ValidateVersion(rawVersion, m.rosaVersions.versions, ocm.DefaultChannelGroup, true, true)
		return err
	}(); err != nil {
		return nil, "", err
	}

	minor := ocm.GetVersionMinor(version)
	role := aws.HCPAccountRoles[aws.HCPInstallerRole]

	// Find all installer roles in the current account using AWS resource tags
	foundRoleARNs, err := m.rClient.AWSClient.FindRoleARNs(aws.HCPInstallerRole, minor)
	if err != nil || foundRoleARNs == nil || len(foundRoleARNs) == 0 {
		metrics.RecordError(errorRosaAWS, m.errorMetric)
		return nil, "", fmt.Errorf("Failed to find %s role: %s", role.Name, err)
	}
	// TODO: this can be hardcoded when we have a permanent account set up
	roleARN := foundRoleARNs[0]
	// Prioritize roles with the default prefix
	for _, rARN := range foundRoleARNs {
		if strings.Contains(rARN, fmt.Sprintf("%s-%s-Role", aws.DefaultPrefix, role.Name)) {
			roleARN = rARN
		}
	}
	// set up other ARNs
	var supportRoleARN, controlPlaneRoleARN, workerRoleARN string
	for roleType, role := range aws.HCPAccountRoles {
		if roleType == aws.HCPInstallerRole {
			// Already dealt with
			continue
		}
		if roleType == aws.ControlPlaneAccountRole {
			// Not needed for Hypershift clusters
			continue
		}
		roleARNs, err := m.rClient.AWSClient.FindRoleARNs(roleType, minor)
		if err != nil {
			metrics.RecordError(errorRosaAWS, m.errorMetric)
			return nil, "", fmt.Errorf("Failed to find %s role: %s", role.Name, err)
		}
		selectedARN := ""
		expectedResourceIDForAccRole := strings.ToLower(fmt.Sprintf("%s-%s-Role", aws.DefaultPrefix, role.Name))
		for _, rARN := range roleARNs {
			resourceId, err := aws.GetResourceIdFromARN(rARN)
			if err != nil {
				metrics.RecordError(errorRosaAWS, m.errorMetric)
				return nil, "", fmt.Errorf("Failed to get resource ID from arn. %s", err)
			}
			lowerCaseResourceIdToCheck := strings.ToLower(resourceId)
			if lowerCaseResourceIdToCheck == expectedResourceIDForAccRole {
				selectedARN = rARN
				break
			}
		}
		if selectedARN == "" {
			metrics.RecordError(errorRosaAWS, m.errorMetric)
			return nil, "", fmt.Errorf("No %s account roles found.", role.Name)
		}
		switch roleType {
		case aws.HCPInstallerRole:
			roleARN = selectedARN
		case aws.HCPSupportRole:
			supportRoleARN = selectedARN
		case aws.ControlPlaneAccountRole:
			controlPlaneRoleARN = selectedARN
		case aws.HCPWorkerRole:
			workerRoleARN = selectedARN
		}
	}

	// combine role arns to list
	roleARNs := []string{
		roleARN,
		supportRoleARN,
		controlPlaneRoleARN,
		workerRoleARN,
	}

	if err := roles.ValidateUnmanagedAccountRoles(roleARNs, m.rClient.AWSClient, version); err != nil {
		metrics.RecordError(errorRosaAWS, m.errorMetric)
		return nil, "", fmt.Errorf("Failed while validating account roles: %s", err)
	}

	operatorRolePath, _ := aws.GetPathFromARN(roleARN)
	operatorIAMRoleList := []ocm.OperatorIAMRole{}
	credRequests, err := m.rClient.OCMClient.GetCredRequests(true)
	if err != nil {
		metrics.RecordError(errorRosaAWS, m.errorMetric)
		return nil, "", fmt.Errorf("Error getting operator credential request from OCM %s", err)
	}
	for _, operator := range credRequests {
		//If the cluster version is less than the supported operator version
		if operator.MinVersion() != "" {
			isSupported, err := ocm.CheckSupportedVersion(ocm.GetVersionMinor(version), operator.MinVersion())
			if err != nil {
				metrics.RecordError(errorRosaOCM, m.errorMetric)
				return nil, "", fmt.Errorf("Error validating operator role '%s' version %s", operator.Name(), err)
			}
			if !isSupported {
				continue
			}
		}
		operatorIAMRoleList = append(operatorIAMRoleList, ocm.OperatorIAMRole{
			Name:      operator.Name(),
			Namespace: operator.Namespace(),
			RoleARN: aws.ComputeOperatorRoleArn(clusterName, operator,
				m.rClient.Creator, operatorRolePath),
		})

	}

	// Get AWS region
	region, err := aws.GetRegion(arguments.GetRegion())
	if err != nil {
		metrics.RecordError(errorRosaAWS, m.errorMetric)
		return nil, "", fmt.Errorf("Error getting region: %v", err)
	}
	dMachineCIDR, dPodCIDR, dServiceCIDR, hostPrefix, _, computeMachineType := m.rClient.OCMClient.GetDefaultClusterFlavors("")
	var machineCIDR, serviceCIDR, podCIDR net.IPNet
	if dMachineCIDR != nil {
		machineCIDR = *dMachineCIDR
	}
	if dServiceCIDR != nil {
		serviceCIDR = *dServiceCIDR
	}
	if dPodCIDR != nil {
		podCIDR = *dPodCIDR
	}

	// reloading updated subnets from a file is fast and not time sensitive, so we can just defer the unlock
	m.rosaSubnets.Lock.RLock()
	defer m.rosaSubnets.Lock.RUnlock()
	// set subnets and identify correct availability zone for private subnet(s)
	subnets, err := m.rClient.AWSClient.ListSubnets()
	if err != nil {
		metrics.RecordError(errorRosaAWS, m.errorMetric)
		return nil, "", fmt.Errorf("Failed to get the list of subnets: %s", err)
	}
	availabilityZones := sets.NewString()
	foundSubnets := 0
	for _, subnet := range subnets {
		if m.rosaSubnets.Subnets.Has(awssdk.StringValue(subnet.SubnetId)) {
			availabilityZones.Insert(awssdk.StringValue(subnet.AvailabilityZone))
			foundSubnets++
		}
	}
	if foundSubnets != len(m.rosaSubnets.Subnets) {
		metrics.RecordError(errorRosaMissingSubnets, m.errorMetric)
		return nil, "", fmt.Errorf("Only found %d subnets out of %d provided subnet IDs", foundSubnets, len(m.rosaSubnets.Subnets))
	}

	no := false
	var expiryTime []byte
	if duration == 0 {
		expiryTime, err = time.Now().Add(m.defaultRosaAge).MarshalText()
		if err != nil {
			return nil, "", fmt.Errorf("Failed to marshal expiry time as text: %w", err)
		}
	} else {
		expiryTime, err = time.Now().Add(duration).MarshalText()
		if err != nil {
			return nil, "", fmt.Errorf("Failed to marshal expiry time as text: %w", err)
		}
	}
	clusterConfig := ocm.Spec{
		Name:                clusterName,
		Region:              region,
		DryRun:              &no,
		Version:             version,
		ComputeMachineType:  computeMachineType,
		MachineCIDR:         machineCIDR,
		ServiceCIDR:         serviceCIDR,
		PodCIDR:             podCIDR,
		HostPrefix:          hostPrefix,
		IsSTS:               true,
		RoleARN:             roleARN,
		SupportRoleARN:      supportRoleARN,
		OperatorIAMRoles:    operatorIAMRoleList,
		ControlPlaneRoleARN: controlPlaneRoleARN,
		WorkerRoleARN:       workerRoleARN,
		Mode:                interactive.ModeAuto,
		ComputeNodes:        2,
		OidcConfigId:        m.rosaOidcConfigId,
		BillingAccount:      m.rosaBillingAccount,
		SubnetIds:           m.rosaSubnets.Subnets.UnsortedList(),
		AvailabilityZones:   availabilityZones.UnsortedList(),
		MultiAZ:             true, // required for Hypershift, even if we only configured 1 AZ
		Hypershift: ocm.Hypershift{
			Enabled: true,
		},
		Tags: map[string]string{
			trimTagName(utils.LaunchLabel): "true",
			utils.UserTag:                  slackID,
			utils.ChannelTag:               slackChannel,
			utils.ExpiryTimeTag:            base64.RawStdEncoding.EncodeToString([]byte(expiryTime)),
		},
		DefaultIngress: ocm.NewDefaultIngressSpec(),
	}

	klog.Infof("Cluster Definition: %+v", clusterConfig)
	klog.Infof("Creating rosa cluster %s", clusterName)
	cluster, err := m.rClient.OCMClient.CreateCluster(clusterConfig)
	if err != nil {
		metrics.RecordError(errorRosaCreate, m.errorMetric)
		return nil, "", fmt.Errorf("Failed to create cluster: %s", err)
	}

	m.rosaClusters.lock.Lock()
	// use an if statement in case the syncRosa function has already added the cluster to the list
	if m.rosaClusters.clusters[cluster.ID()] == nil {
		if m.rosaClusters.clusters == nil {
			m.rosaClusters.clusters = map[string]*clustermgmtv1.Cluster{}
		}
		m.rosaClusters.clusters[cluster.ID()] = cluster
	}

	// add new cluster to configmap before creating operator-roles and oidc-provider
	klog.Infof("Updating rosa clusters secret")
	if err := utils.UpdateSecret(RosaClusterSecretName, m.rosaSecretClient, func(secret *corev1.Secret) {
		secret.Data[cluster.ID()] = []byte("")
	}); err != nil {
		m.rosaClusters.lock.Unlock()
		metrics.RecordError(errorRosaUpdateSecret, m.errorMetric)
		return cluster, "", fmt.Errorf("Failed to update `%s` secret to add cluster %s to list after 10 retries", RosaClusterSecretName, cluster.ID())
	}
	m.rosaClusters.lock.Unlock()
	klog.Infof("Creating operator-roles and oidc-provider for cluster %s", cluster.ID())
	rolesCMD := fmt.Sprintf("rosa create operator-roles --cluster %s --yes --mode auto", cluster.ID())
	oidcCMD := fmt.Sprintf("rosa create oidc-provider --cluster %s --yes --mode auto", cluster.ID())
	rolesOutput := exec.Command(strings.Split(rolesCMD, " ")[0], strings.Split(rolesCMD, " ")[1:]...)
	klog.Infof("Running %s\n", rolesOutput.String())
	if err := rolesOutput.Run(); err != nil {
		metrics.RecordError(errorRosaRoles, m.errorMetric)
		return nil, "", fmt.Errorf("Failed to run command: %v", err)
	}
	oidcOutput := exec.Command(strings.Split(oidcCMD, " ")[0], strings.Split(oidcCMD, " ")[1:]...)
	klog.Infof("Running %s\n", oidcOutput.String())
	if err := oidcOutput.Run(); err != nil {
		metrics.RecordError(errorRosaRoles, m.errorMetric)
		return nil, "", fmt.Errorf("Failed to run command: %v", err)
	}
	klog.Infof("Created rosa roles and oidc for %s", cluster.ID())
	// update clusters list
	go m.rosaSync() // nolint:errcheck
	return cluster, version, nil
}

func (m *jobManager) deleteCluster(clusterID string) error {
	klog.Infof("Deleting cluster '%s'", clusterID)
	_, err := m.rClient.OCMClient.DeleteCluster(clusterID, false, m.rClient.Creator)
	if err != nil {
		metrics.RecordError(errorRosaDelete, m.errorMetric)
		return fmt.Errorf("%s", err)
	}
	klog.Infof("Cluster '%s' will start uninstalling now", clusterID)
	return nil
}

func (m *jobManager) removeAssociatedAWSResources(clusterID string) error {
	rolesCMD := fmt.Sprintf("rosa delete operator-roles --cluster %s --yes --mode auto", clusterID)
	oidcCMD := fmt.Sprintf("rosa delete oidc-provider --cluster %s --yes --mode auto", clusterID)
	rolesOutput := exec.Command(strings.Split(rolesCMD, " ")[0], strings.Split(rolesCMD, " ")[1:]...)
	klog.Infof("Running %s\n", rolesOutput.String())
	if err := rolesOutput.Run(); err != nil {
		metrics.RecordError(errorRosaCleanup, m.errorMetric)
		return fmt.Errorf("Failed to run command: %v", err)
	}
	oidcOutput := exec.Command(strings.Split(oidcCMD, " ")[0], strings.Split(oidcCMD, " ")[1:]...)
	klog.Infof("Running %s\n", oidcOutput.String())
	if err := oidcOutput.Run(); err != nil {
		metrics.RecordError(errorRosaCleanup, m.errorMetric)
		return fmt.Errorf("Failed to run command: %v", err)
	}
	klog.Infof("Deleted rosa roles and oidc for %s", clusterID)
	return nil
}

func (m *jobManager) waitForConsole(cluster *clustermgmtv1.Cluster, readyTime time.Time) error {
	for i := 0; i < 20; i++ {
		updatedCluster, err := m.rClient.OCMClient.GetCluster(cluster.ID(), m.rClient.Creator)
		if err != nil {
			metrics.RecordError(errorRosaGetSingle, m.errorMetric)
			return err
		}
		if _, ok := updatedCluster.GetConsole(); ok {
			rosaConsoleTimeMetric.Observe(time.Since(cluster.CreationTimestamp()).Minutes())
			rosaReadyToConsoleTimeMetric.Observe(time.Since(readyTime).Minutes())
			klog.Infof("Console for %s became ready", cluster.ID())
			return nil
		}
		klog.Infof("Console for %s not ready yet", cluster.ID())
		time.Sleep(time.Minute)
	}
	metrics.RecordError(errorRosaConsole, m.errorMetric)
	return fmt.Errorf("Console URL never became available")
}

// addClusterAuthAndWait is a wrapper for addClusterAuth that sleeps until the new auth is active
func (m *jobManager) addClusterAuthAndWait(cluster *clustermgmtv1.Cluster, readyTime time.Time) (bool, error) {
	password, alreadyExists, err := m.addClusterAuth(cluster.ID())
	if err != nil || alreadyExists {
		return alreadyExists, err
	}
	clientConfig := rest.Config{
		Host:     cluster.API().URL(),
		Username: m.rosaClusterAdminUsername,
		Password: password,
	}
	authReady := false
	for i := 0; i < 10; i++ {
		if _, err := tokencmd.RequestToken(&clientConfig, nil, m.rosaClusterAdminUsername, password); err != nil {
			if k8serrors.IsUnauthorized(err) || errors.Is(err, io.EOF) || errors.Is(err, os.ErrDeadlineExceeded) {
				klog.Infof("Cluster auth for %s not ready yet", cluster.ID())
				time.Sleep(time.Minute)
			} else {
				metrics.RecordError(errorRosaAuth, m.errorMetric)
				return false, err
			}
		} else {
			authReady = true
			break
		}
	}
	if !authReady {
		metrics.RecordError(errorRosaAuth, m.errorMetric)
		return false, fmt.Errorf("Cluster auth never became ready")
	}
	rosaAuthTimeMetric.Observe(time.Since(cluster.CreationTimestamp()).Minutes())
	rosaReadyToAuthTimeMetric.Observe(time.Since(readyTime).Minutes())
	klog.Infof("Cluster auth for %s became ready", cluster.ID())
	// hosted clusters become ready and accept the new auth before the workers are done
	// and a console URL exists. We should wait for the console to be ready.
	return false, nil
}

func (m *jobManager) addClusterAuth(clusterID string) (string, bool, error) {
	// Try to find an existing htpasswd identity provider and
	// check if cluster-admin user already exists
	idps, err := m.rClient.OCMClient.GetIdentityProviders(clusterID)
	if err != nil {
		return "", false, fmt.Errorf("Failed to get identity providers for cluster '%s': %v", clusterID, err)
	}

	for _, item := range idps {
		if ocm.IdentityProviderType(item) == ocm.HTPasswdIDPType {
			return "", true, nil
		}
	}
	password, err := generateRandomString(23, false)
	if err != nil {
		return "", false, fmt.Errorf("Failed to generate a random password: %w", err)
	}

	// Add cluster-admin user to the cluster-admins group
	klog.Infof("Adding '%s' user to cluster '%s'", m.rosaClusterAdminUsername, clusterID)
	user, err := clustermgmtv1.NewUser().ID(m.rosaClusterAdminUsername).Build()
	if err != nil {
		metrics.RecordError(errorRosaGetIDP, m.errorMetric)
		return "", false, fmt.Errorf("Failed to create user '%s' for cluster '%s'", m.rosaClusterAdminUsername, clusterID)
	}

	_, err = m.rClient.OCMClient.CreateUser(clusterID, "cluster-admins", user)
	if err != nil {
		metrics.RecordError(errorRosaCreateUser, m.errorMetric)
		return "", false, fmt.Errorf("Failed to add user '%s' to cluster '%s': %s", m.rosaClusterAdminUsername, clusterID, err)
	}

	klog.Infof("Adding 'htpasswd' idp to cluster '%s'", clusterID)
	htpasswdIDP := clustermgmtv1.NewHTPasswdIdentityProvider().Users(clustermgmtv1.NewHTPasswdUserList().Items(
		CreateHTPasswdUserBuilder(m.rosaClusterAdminUsername, password),
	))
	newIDP, err := clustermgmtv1.NewIdentityProvider().
		Type(clustermgmtv1.IdentityProviderTypeHtpasswd).
		Name("htpasswd").
		Htpasswd(htpasswdIDP).
		Build()
	if err != nil {
		metrics.RecordError(errorRosaBuildIDP, m.errorMetric)
		return "", false, fmt.Errorf("Failed to build 'htpasswd' identity provider for cluster '%s'", clusterID)
	}

	// Add HTPasswd IDP to cluster:
	_, err = m.rClient.OCMClient.CreateIdentityProvider(clusterID, newIDP)
	if err != nil {
		metrics.RecordError(errorRosaCreateIDP, m.errorMetric)
		return "", false, fmt.Errorf("Failed to add 'htpasswd' identity provider to cluster '%s': %v", clusterID, err)
	}

	// update secret with new passwd
	klog.Infof("Updating rosa clusters secret with password for %s", clusterID)
	if err := utils.UpdateSecret(RosaClusterSecretName, m.rosaSecretClient, func(secret *corev1.Secret) {
		secret.Data[clusterID] = []byte(password)
	}); err != nil {
		metrics.RecordError(errorRosaUpdateSecret, m.errorMetric)
		return "", false, fmt.Errorf("Failed to update `%s` secret to add cluster %s to list after 10 retries", RosaClusterSecretName, clusterID)
	}
	klog.Infof("Updated rosa clusters secret with password for %s", clusterID)
	m.rosaClusters.lock.Lock()
	defer m.rosaClusters.lock.Unlock()
	if m.rosaClusters.clusterPasswords == nil {
		m.rosaClusters.clusterPasswords = map[string]string{}
	}
	m.rosaClusters.clusterPasswords[clusterID] = password
	return password, false, nil
}

func (m *jobManager) getROSAClusterForUser(username string) (*clustermgmtv1.Cluster, string) {
	m.rosaClusters.lock.RLock()
	defer m.rosaClusters.lock.RUnlock()
	for _, cluster := range m.rosaClusters.clusters {
		if cluster.State() != clustermgmtv1.ClusterStateUninstalling && cluster.AWS().Tags() != nil && cluster.AWS().Tags()[utils.UserTag] == username {
			return cluster, m.rosaClusters.clusterPasswords[cluster.ID()]
		}
	}
	return nil, ""
}

func (m *jobManager) getROSAClusterByName(name string) *clustermgmtv1.Cluster {
	m.rosaClusters.lock.RLock()
	defer m.rosaClusters.lock.RUnlock()
	for _, cluster := range m.rosaClusters.clusters {
		if cluster.State() != clustermgmtv1.ClusterStateUninstalling && cluster.Name() == name {
			return cluster
		}
	}
	return nil
}

func (m *jobManager) describeROSACluster(name string) (string, error) {
	if cluster := m.getROSAClusterByName(name); cluster != nil {
		describeCMD := fmt.Sprintf("rosa describe cluster --cluster=%s", cluster.Name())
		cmd := exec.Command(strings.Split(describeCMD, " ")[0], strings.Split(describeCMD, " ")[1:]...)
		klog.Infof("Running %s\n", cmd.String())
		out, err := cmd.Output()
		if err != nil {
			metrics.RecordError(errorRosaDescribe, m.errorMetric)
			return "", fmt.Errorf("Failed to run command: %v", err)
		}
		return fmt.Sprintf("`%s` returned:\n```%s```", cmd.String(), string(out)), nil
	}
	return "", fmt.Errorf("Unable to locate cluster named: %s", name)
}

// based on github.com/openshift/rosa/cmd/create/admin/cmd.go
func generateRandomString(length int, onlyLower bool) (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
	)
	var all string
	if onlyLower {
		all = lowerLetters + digits
	} else {
		all = lowerLetters + upperLetters + digits
	}
	var password string
	for i := 1; i < length-1; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		n, err = rand.Int(rand.Reader, big.NewInt(int64(len(password)+1)))
		if err != nil {
			return "", err
		}
		j := n.Int64()
		password = password[0:j] + newchar + password[j:]
	}

	// cluster names must start with letter
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(lowerLetters))))
	if err != nil {
		return "", err
	}
	password = string(lowerLetters[n.Int64()]) + password

	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		// last character cannot be a `-`
		if replace > len(password)-2 {
			break
		}
		pw[replace] = '-'
	}

	return string(pw), nil
}

func CreateHTPasswdUserBuilder(username, password string) *clustermgmtv1.HTPasswdUserBuilder {
	builder := clustermgmtv1.NewHTPasswdUser()
	if username != "" {
		builder = builder.Username(username)
	}
	if password != "" {
		builder = builder.Password(password)
	}
	return builder
}

// trimTagName removes `.openshift.io` from a string to make it a usable tag for AWS
func trimTagName(tagName string) string {
	return strings.Replace(tagName, ".openshift.io", "", 1)
}
