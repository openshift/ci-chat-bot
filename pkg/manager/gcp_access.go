package manager

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/bigquery"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
	"k8s.io/klog"
)

const (
	// GCP project ID where users need IAM access
	GCPProjectID = "openshift-crt-ephemeral-access"

	// IAM role to grant to users for GCP workspace access
	// This is a custom project-level role, so it must use the full resource path
	GCPIAMRole = "projects/openshift-crt-ephemeral-access/roles/chat_bot_user"

	// Duration for which access is valid (7 days)
	GCPAccessDuration = 7 * 24 * time.Hour

	// Duration before automatic resource cleanup by external GCP process (7 days)
	// Note: This constant is informational only - cleanup is handled by an external
	// process, not the ci-chat-bot. Service accounts are cleaned up after 7-day
	// access expiration via the bot's hourly cleanup job.
	GCPResourceCleanupDuration = 7 * 24 * time.Hour

	// BigQuery dataset and table for audit logging
	BigQueryDataset = "ci_chat_bot"
	BigQueryTable   = "access_grants"
)

var (
	// timestampRegex extracts timestamp from IAM condition expression
	// Matches: request.time < timestamp('2026-01-19T12:34:56Z')
	timestampRegex = regexp.MustCompile(`timestamp\('([^']+)'\)`)
)

// GCPAccessManager handles adding/removing users from GCP project IAM
type GCPAccessManager struct {
	crmService *cloudresourcemanager.Service
	iamService *iam.Service
	bqClient   *bigquery.Client
	projectID  string
	iamRole    string
	mutex      sync.RWMutex
	enabled    bool
	bqEnabled  bool
	bqDataset  string
	bqTable    string
	dryRun     bool // If true, skip IAM changes but still log to BigQuery
	// Runtime cache of active grants (email -> grant info)
	// Populated from GCP IAM policy on demand
	grantsCache map[string]*UserAccessGrant
}

// UserAccessGrant represents a user's access grant
type UserAccessGrant struct {
	Email               string    `json:"email"`
	GrantedAt           time.Time `json:"granted_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	RequestedBy         string    `json:"requested_by"`          // Slack user ID
	Justification       string    `json:"justification"`         // Business justification for access
	ServiceAccountEmail string    `json:"service_account_email"` // GCP service account email
}

// AccessGrantLogEntry represents a row in the BigQuery audit log table
type AccessGrantLogEntry struct {
	Timestamp           time.Time `bigquery:"timestamp"`
	Command             string    `bigquery:"command"`
	UserEmail           string    `bigquery:"user_email"`
	SlackUserID         string    `bigquery:"slack_user_id"`
	Justification       string    `bigquery:"justification"`
	Resource            string    `bigquery:"resource"`
	ProjectID           string    `bigquery:"project_id"`
	ExpiresAt           time.Time `bigquery:"expires_at"`
	ServiceAccountEmail string    `bigquery:"service_account_email"`
}

// NewGCPAccessManager creates a new GCP access manager
func NewGCPAccessManager(serviceAccountJSON string, dryRun bool) (*GCPAccessManager, error) {
	manager := &GCPAccessManager{
		projectID:   GCPProjectID,
		iamRole:     GCPIAMRole,
		enabled:     false,
		bqEnabled:   false,
		bqDataset:   BigQueryDataset,
		bqTable:     BigQueryTable,
		dryRun:      dryRun,
		grantsCache: make(map[string]*UserAccessGrant),
	}

	// If service account credentials are not provided, disable the manager
	if serviceAccountJSON == "" {
		klog.Warning("GCP access manager disabled: service account credentials not provided")
		return manager, nil
	}

	ctx := context.Background()

	// Parse service account JSON
	config, err := google.JWTConfigFromJSON(
		[]byte(serviceAccountJSON),
		cloudresourcemanager.CloudPlatformScope,
		bigquery.Scope,
		iam.CloudPlatformScope,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service account JSON: %w", err)
	}

	// Create Cloud Resource Manager service
	crmService, err := cloudresourcemanager.NewService(ctx, option.WithHTTPClient(config.Client(ctx)))
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud resource manager service: %w", err)
	}

	// Create IAM service for service account management
	iamService, err := iam.NewService(ctx, option.WithHTTPClient(config.Client(ctx)))
	if err != nil {
		return nil, fmt.Errorf("failed to create IAM service: %w", err)
	}

	manager.crmService = crmService
	manager.iamService = iamService
	manager.enabled = true

	// Create BigQuery client for audit logging
	bqClient, err := bigquery.NewClient(ctx, GCPProjectID, option.WithHTTPClient(config.Client(ctx)))
	if err != nil {
		klog.Warningf("Failed to create BigQuery client: %v. Audit logging will be disabled.", err)
	} else {
		manager.bqClient = bqClient
		manager.bqEnabled = true
		klog.Info("BigQuery audit logging enabled")
	}

	if dryRun {
		klog.Warning("GCP access manager running in DRY-RUN mode - IAM changes will be skipped")
	}

	klog.Info("GCP access manager initialized successfully")
	return manager, nil
}

// IsEnabled returns whether the GCP access manager is enabled
func (m *GCPAccessManager) IsEnabled() bool {
	return m.enabled
}

// createServiceAccount creates a new service account for the user
func (m *GCPAccessManager) createServiceAccount(ctx context.Context, email, justification string) (*iam.ServiceAccount, error) {
	// Sanitize username using reversible encoding (assumes @redhat.com domain)
	sanitized := sanitizeUsernameReversible(email)

	// Add timestamp for uniqueness
	timestamp := time.Now().Unix()

	// GCP service account names must be 6-30 characters
	// Format: {sanitized-username}-{timestamp}
	// Reserve 11 characters for dash and 10-digit timestamp
	maxUsernameLength := 30 - 11
	if len(sanitized) > maxUsernameLength {
		// If username is too long even after encoding, fall back to DisplayName
		klog.Warningf("Username from %s is too long (%d chars) for reversible encoding, using DisplayName fallback", email, len(sanitized))
		// Use old non-reversible sanitization for compatibility
		sanitized = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(email, "@", "-"), ".", "-"))
		if len(sanitized) > maxUsernameLength {
			sanitized = sanitized[:maxUsernameLength]
		}
	}

	accountID := fmt.Sprintf("%s-%d", sanitized, timestamp)

	request := &iam.CreateServiceAccountRequest{
		AccountId: accountID,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: email,
			Description: fmt.Sprintf("Temporary access granted via ci-chat-bot. Justification: %s", justification),
		},
	}

	projectName := fmt.Sprintf("projects/%s", m.projectID)
	serviceAccount, err := m.iamService.Projects.ServiceAccounts.Create(projectName, request).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to create service account: %w", err)
	}

	klog.Infof("Created service account: %s", serviceAccount.Email)
	return serviceAccount, nil
}

// createServiceAccountKey generates a new key for the service account
// Retries on 404 errors to handle GCP eventual consistency
func (m *GCPAccessManager) createServiceAccountKey(ctx context.Context, serviceAccountEmail string) ([]byte, error) {
	request := &iam.CreateServiceAccountKeyRequest{
		PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE",
	}

	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", m.projectID, serviceAccountEmail)

	// Retry logic to handle GCP eventual consistency
	// After creating a service account, it may take a few seconds to propagate
	maxRetries := 10
	baseDelay := 1 * time.Second
	var key *iam.ServiceAccountKey
	var err error

	for attempt := range maxRetries {
		key, err = m.iamService.Projects.ServiceAccounts.Keys.Create(resourceName, request).Context(ctx).Do()
		if err == nil {
			// Success
			break
		}

		// Check if this is a 404 error (service account not found yet)
		if isNotFoundError(err) {
			// Calculate exponential backoff delay
			delay := min(baseDelay*time.Duration(1<<uint(attempt)),
				// Cap at 16 seconds
				16*time.Second)

			klog.V(2).Infof("Service account %s not yet available (attempt %d/%d), retrying in %v", serviceAccountEmail, attempt+1, maxRetries, delay)
			time.Sleep(delay)
			continue
		}

		// For non-404 errors, fail immediately
		return nil, fmt.Errorf("failed to create service account key: %w", err)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create service account key after %d attempts: %w", maxRetries, err)
	}

	// The key data is base64 encoded, decode it
	keyJSON, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode key data: %w", err)
	}

	klog.Infof("Created key for service account: %s", serviceAccountEmail)
	return keyJSON, nil
}

// deleteServiceAccount deletes a service account
func (m *GCPAccessManager) deleteServiceAccount(ctx context.Context, serviceAccountEmail string) error {
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", m.projectID, serviceAccountEmail)
	_, err := m.iamService.Projects.ServiceAccounts.Delete(resourceName).Context(ctx).Do()
	if err != nil {
		// Handle "not found" errors gracefully
		if isNotFoundError(err) {
			klog.V(2).Infof("Service account already deleted: %s", serviceAccountEmail)
			return nil
		}
		return fmt.Errorf("failed to delete service account: %w", err)
	}

	klog.Infof("Deleted service account: %s", serviceAccountEmail)
	return nil
}

// logAccessGrant logs an access grant to BigQuery for audit purposes
func (m *GCPAccessManager) logAccessGrant(ctx context.Context, email, slackUserID, justification, resource, serviceAccountEmail string, expiresAt time.Time) error {
	if !m.bqEnabled {
		klog.V(2).Info("BigQuery logging disabled, skipping audit log")
		return nil
	}

	entry := &AccessGrantLogEntry{
		Timestamp:           time.Now(),
		Command:             "request",
		UserEmail:           email,
		SlackUserID:         slackUserID,
		Justification:       justification,
		Resource:            resource,
		ProjectID:           m.projectID,
		ExpiresAt:           expiresAt,
		ServiceAccountEmail: serviceAccountEmail,
	}

	inserter := m.bqClient.Dataset(m.bqDataset).Table(m.bqTable).Inserter()
	if err := inserter.Put(ctx, entry); err != nil {
		return fmt.Errorf("failed to insert audit log to BigQuery: %w", err)
	}

	klog.Infof("Logged access grant to BigQuery: user=%s, resource=%s, service_account=%s", email, resource, serviceAccountEmail)
	return nil
}

// GrantAccess creates a service account for the user and grants it IAM access with a time-based condition
// Returns the service account key as JSON bytes for upload to Slack
func (m *GCPAccessManager) GrantAccess(email, requestedBy, justification, resource string) ([]byte, time.Time, error) {
	if !m.enabled {
		return nil, time.Time{}, fmt.Errorf("GCP access manager is not enabled. Please contact an administrator to configure GCP service account")
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	ctx := context.Background()
	now := time.Now()

	// Check if user already has active access in cache
	if grant, exists := m.grantsCache[email]; exists {
		// If already granted and not expired, regenerate key for existing service account
		if grant.ExpiresAt.After(now) {
			klog.Infof("User %s already has active access, regenerating key", email)

			var keyJSON []byte
			var err error

			// In dry-run mode, return mock key
			if m.dryRun {
				keyJSON = []byte(`{"type":"service_account","project_id":"dry-run"}`)
			} else {
				// Regenerate key for existing service account
				keyJSON, err = m.createServiceAccountKey(ctx, grant.ServiceAccountEmail)
				if err != nil {
					return nil, time.Time{}, fmt.Errorf("failed to regenerate key: %w", err)
				}
			}

			return keyJSON, grant.ExpiresAt, nil
		}
	}

	// Calculate expiration time
	expiresAt := now.Add(GCPAccessDuration)
	var serviceAccountEmail string
	var keyJSON []byte

	// In dry-run mode, skip service account creation and IAM policy changes
	if m.dryRun {
		klog.Infof("DRY-RUN: Would create service account for user %s (role: %s, expires: %s)", email, m.iamRole, expiresAt.Format(time.RFC3339))
		serviceAccountEmail = fmt.Sprintf("dry-run-%s@%s.iam.gserviceaccount.com", strings.ReplaceAll(email, "@", "-at-"), m.projectID)
		keyJSON = []byte(`{"type":"service_account","project_id":"dry-run"}`)
	} else {
		// Create service account
		serviceAccount, err := m.createServiceAccount(ctx, email, justification)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("failed to create service account: %w", err)
		}
		serviceAccountEmail = serviceAccount.Email

		// Generate service account key
		keyJSON, err = m.createServiceAccountKey(ctx, serviceAccountEmail)
		if err != nil {
			// Rollback: delete the service account
			if delErr := m.deleteServiceAccount(ctx, serviceAccountEmail); delErr != nil {
				klog.Errorf("Failed to delete service account after key generation failure: %v", delErr)
			}
			return nil, time.Time{}, fmt.Errorf("failed to generate service account key: %w", err)
		}

		// Get current IAM policy
		// Request version 3 to support conditional bindings
		policy, err := m.crmService.Projects.GetIamPolicy(m.projectID, &cloudresourcemanager.GetIamPolicyRequest{
			Options: &cloudresourcemanager.GetPolicyOptions{
				RequestedPolicyVersion: 3,
			},
		}).Context(ctx).Do()
		if err != nil {
			// Rollback: delete the service account
			if delErr := m.deleteServiceAccount(ctx, serviceAccountEmail); delErr != nil {
				klog.Errorf("Failed to delete service account after IAM policy failure: %v", delErr)
			}
			return nil, time.Time{}, fmt.Errorf("failed to get IAM policy: %w", err)
		}

		// Add service account as IAM member with a time-based condition
		member := "serviceAccount:" + serviceAccountEmail

		// Create a new binding with condition for this service account
		condition := &cloudresourcemanager.Expr{
			Title:       "Temp Access",
			Description: fmt.Sprintf("Access expires on %s. Justification: %s", expiresAt.Format(time.RFC3339), justification),
			Expression:  fmt.Sprintf("request.time < timestamp('%s')", expiresAt.Format(time.RFC3339)),
		}

		binding := &cloudresourcemanager.Binding{
			Role:      m.iamRole,
			Members:   []string{member},
			Condition: condition,
		}

		policy.Bindings = append(policy.Bindings, binding)

		// Ensure policy version is set to 3 for conditional bindings
		policy.Version = 3

		// Set the updated IAM policy
		setRequest := &cloudresourcemanager.SetIamPolicyRequest{
			Policy: policy,
		}
		_, err = m.crmService.Projects.SetIamPolicy(m.projectID, setRequest).Context(ctx).Do()
		if err != nil {
			// Rollback: delete the service account
			if delErr := m.deleteServiceAccount(ctx, serviceAccountEmail); delErr != nil {
				klog.Errorf("Failed to delete service account after IAM policy set failure: %v", delErr)
			}
			return nil, time.Time{}, fmt.Errorf("failed to set IAM policy: %w", err)
		}
	}

	// Log to BigQuery for audit purposes
	if err := m.logAccessGrant(ctx, email, requestedBy, justification, resource, serviceAccountEmail, expiresAt); err != nil {
		// Log the error but don't fail the grant operation
		klog.Warningf("Failed to log access grant to BigQuery: %v", err)
	}

	// Update cache
	grant := &UserAccessGrant{
		Email:               email,
		GrantedAt:           now,
		ExpiresAt:           expiresAt,
		RequestedBy:         requestedBy,
		Justification:       justification,
		ServiceAccountEmail: serviceAccountEmail,
	}
	m.grantsCache[email] = grant

	if m.dryRun {
		klog.Infof("DRY-RUN: Granted GCP IAM access to service account %s for user %s, expires at %s", serviceAccountEmail, email, grant.ExpiresAt)
	} else {
		klog.Infof("Granted GCP IAM access to service account %s for user %s, expires at %s", serviceAccountEmail, email, grant.ExpiresAt)
	}
	return keyJSON, expiresAt, nil
}

// getUserGrantFromIAM queries IAM policy to find a user's grant
// Must be called without holding the mutex
func (m *GCPAccessManager) getUserGrantFromIAM(ctx context.Context, email string) (*UserAccessGrant, error) {
	// In dry-run mode or if crmService is nil, we can't query IAM
	if m.crmService == nil {
		return nil, nil
	}

	policy, err := m.crmService.Projects.GetIamPolicy(m.projectID, &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{
			RequestedPolicyVersion: 3,
		},
	}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get IAM policy: %w", err)
	}

	// Look for service accounts in conditional bindings
	for _, binding := range policy.Bindings {
		if binding.Role == m.iamRole && binding.Condition != nil && binding.Condition.Title == "Temp Access" {
			// Check each service account member
			for _, member := range binding.Members {
				serviceAccountEmail := extractServiceAccountEmail(member)
				if serviceAccountEmail == "" {
					continue
				}

				// Try to extract user email from service account email (preferred - no API call needed)
				userEmail := extractUserEmailFromServiceAccount(serviceAccountEmail)

				if userEmail == email {
					// Found via email parsing - most secure method (based on immutable SA email)
					klog.V(2).Infof("Found grant for %s via service account email parsing", email)

					// Parse expiration from condition
					expiresAt, err := parseExpirationFromCondition(binding.Condition)
					if err != nil {
						klog.Warningf("Failed to parse expiration for user %s: %v", email, err)
						continue
					}

					// Create grant from IAM policy data with ALL fields populated
					return &UserAccessGrant{
						Email:               email,
						ExpiresAt:           expiresAt,
						GrantedAt:           expiresAt.Add(-GCPAccessDuration),
						ServiceAccountEmail: serviceAccountEmail,
						// RequestedBy and Justification are not available from IAM policy
					}, nil
				}

				// Fallback: Query service account and check DisplayName
				// This handles usernames that were too long for reversible encoding
				// Only query if the email we're looking for would have needed the fallback
				if needsDisplayNameFallback(email) {
					sa, err := m.getServiceAccount(ctx, serviceAccountEmail)
					if err != nil {
						klog.Warningf("Failed to get service account %s: %v", serviceAccountEmail, err)
						continue
					}

					if sa.DisplayName == email {
						klog.V(2).Infof("Found grant for %s via DisplayName fallback", email)

						// Parse expiration from condition
						expiresAt, err := parseExpirationFromCondition(binding.Condition)
						if err != nil {
							klog.Warningf("Failed to parse expiration for user %s: %v", email, err)
							continue
						}

						// Create grant from IAM policy data with ALL fields populated
						return &UserAccessGrant{
							Email:               email,
							ExpiresAt:           expiresAt,
							GrantedAt:           expiresAt.Add(-GCPAccessDuration),
							ServiceAccountEmail: serviceAccountEmail,
							// RequestedBy and Justification are not available from IAM policy
						}, nil
					}
				}
			}
		}
	}

	return nil, nil
}

// RevokeAccess removes a service account and its IAM binding for a user
func (m *GCPAccessManager) RevokeAccess(email string) error {
	if !m.enabled {
		return fmt.Errorf("GCP access manager is not enabled")
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	ctx := context.Background()

	// Get service account email from cache
	grant, exists := m.grantsCache[email]
	var serviceAccountEmail string
	if exists {
		serviceAccountEmail = grant.ServiceAccountEmail
	}

	// If not in cache, query IAM policy to find it
	if serviceAccountEmail == "" {
		// Temporarily unlock to query IAM without holding the lock
		m.mutex.Unlock()
		grant, err := m.getUserGrantFromIAM(ctx, email)
		m.mutex.Lock()

		if err != nil {
			klog.Warningf("Failed to find service account for user %s: %v", email, err)
		}
		if grant != nil {
			serviceAccountEmail = grant.ServiceAccountEmail
			// Update cache with discovered grant
			m.grantsCache[email] = grant
		}
	}

	// If still not found, user has no active access
	if serviceAccountEmail == "" {
		klog.V(2).Infof("No service account found for user %s, nothing to revoke", email)
		delete(m.grantsCache, email)
		return nil
	}

	// In dry-run mode, skip service account deletion and IAM policy changes
	if m.dryRun {
		klog.Infof("DRY-RUN: Would revoke GCP IAM access for user %s (service account: %s)", email, serviceAccountEmail)
		delete(m.grantsCache, email)
		return nil
	}

	// Get current IAM policy
	policy, err := m.crmService.Projects.GetIamPolicy(m.projectID, &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{
			RequestedPolicyVersion: 3,
		},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get IAM policy: %w", err)
	}

	// Remove conditional bindings for this service account
	member := "serviceAccount:" + serviceAccountEmail

	newBindings := []*cloudresourcemanager.Binding{}
	modified := false

	for _, binding := range policy.Bindings {
		if binding.Role == m.iamRole && binding.Condition != nil && binding.Condition.Title == "Temp Access" {
			// Check if this binding contains the service account
			if slices.Contains(binding.Members, member) {
				// Remove the service account from this binding
				binding.Members = slices.DeleteFunc(binding.Members, func(m string) bool {
					return m == member
				})
				modified = true

				// Only keep the binding if it still has members
				if len(binding.Members) > 0 {
					newBindings = append(newBindings, binding)
				}
			} else {
				newBindings = append(newBindings, binding)
			}
		} else {
			newBindings = append(newBindings, binding)
		}
	}

	// Set the updated IAM policy if modified
	if modified {
		policy.Bindings = newBindings
		// Ensure policy version is set to 3 for conditional bindings
		policy.Version = 3
		setRequest := &cloudresourcemanager.SetIamPolicyRequest{
			Policy: policy,
		}
		_, err = m.crmService.Projects.SetIamPolicy(m.projectID, setRequest).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to set IAM policy: %w", err)
		}
	}

	// Delete the service account
	if err := m.deleteServiceAccount(ctx, serviceAccountEmail); err != nil {
		klog.Warningf("Failed to delete service account %s: %v", serviceAccountEmail, err)
		// Don't fail the entire revoke operation if service account deletion fails
	}

	// Update cache
	delete(m.grantsCache, email)

	klog.Infof("Revoked GCP IAM access for user %s (service account: %s)", email, serviceAccountEmail)
	return nil
}

// CleanupExpiredAccess removes users whose access has expired
func (m *GCPAccessManager) CleanupExpiredAccess() error {
	if !m.enabled {
		return nil
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	ctx := context.Background()
	now := time.Now()

	// Get current IAM policy to find expired bindings
	policy, err := m.crmService.Projects.GetIamPolicy(m.projectID, &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{
			RequestedPolicyVersion: 3,
		},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get IAM policy: %w", err)
	}

	// Find expired bindings by parsing condition timestamps
	newBindings := []*cloudresourcemanager.Binding{}
	modified := false
	expiredCount := 0
	expiredServiceAccounts := []string{}

	for _, binding := range policy.Bindings {
		if binding.Role == m.iamRole && binding.Condition != nil && binding.Condition.Title == "Temp Access" {
			// Extract expiration timestamp from condition expression
			expiresAt, err := parseExpirationFromCondition(binding.Condition)
			if err != nil {
				klog.Warningf("Failed to parse expiration from condition: %v", err)
				newBindings = append(newBindings, binding)
				continue
			}

			// If binding is expired, don't include it
			if expiresAt.Before(now) {
				modified = true
				expiredCount++
				// Collect service accounts to delete and remove from cache
				for _, member := range binding.Members {
					// Extract service account email
					serviceAccountEmail := extractServiceAccountEmail(member)
					if serviceAccountEmail != "" {
						expiredServiceAccounts = append(expiredServiceAccounts, serviceAccountEmail)
						klog.Infof("Found expired service account: %s", serviceAccountEmail)

						// Remove from cache (look up by user email from cache)
						for userEmail, grant := range m.grantsCache {
							if grant.ServiceAccountEmail == serviceAccountEmail {
								delete(m.grantsCache, userEmail)
								klog.Infof("Removed expired GCP IAM access for user %s", userEmail)
							}
						}
					}
				}
			} else {
				newBindings = append(newBindings, binding)
			}
		} else {
			newBindings = append(newBindings, binding)
		}
	}

	if expiredCount == 0 {
		return nil
	}

	klog.Infof("Found %d expired access grants to clean up", expiredCount)

	// Set the updated IAM policy if modified
	if modified {
		policy.Bindings = newBindings
		// Ensure policy version is set to 3 for conditional bindings
		policy.Version = 3
		setRequest := &cloudresourcemanager.SetIamPolicyRequest{
			Policy: policy,
		}
		_, err = m.crmService.Projects.SetIamPolicy(m.projectID, setRequest).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to set IAM policy: %w", err)
		}
	}

	// Delete expired service accounts
	for _, serviceAccountEmail := range expiredServiceAccounts {
		if err := m.deleteServiceAccount(ctx, serviceAccountEmail); err != nil {
			klog.Warningf("Failed to delete expired service account %s: %v", serviceAccountEmail, err)
			// Continue with other service accounts even if one fails
		}
	}

	return nil
}

// getServiceAccount retrieves a service account by email
func (m *GCPAccessManager) getServiceAccount(ctx context.Context, serviceAccountEmail string) (*iam.ServiceAccount, error) {
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", m.projectID, serviceAccountEmail)
	return m.iamService.Projects.ServiceAccounts.Get(resourceName).Context(ctx).Do()
}

// GetUserGrant returns the grant information for a user, if it exists
// It checks the cache first, then queries GCP IAM policy if not cached
func (m *GCPAccessManager) GetUserGrant(email string) (*UserAccessGrant, error) {
	if !m.enabled {
		return nil, fmt.Errorf("GCP access manager is not enabled")
	}

	m.mutex.RLock()
	// Check cache first
	if grant, exists := m.grantsCache[email]; exists {
		m.mutex.RUnlock()
		return grant, nil
	}
	m.mutex.RUnlock()

	// Not in cache, query GCP IAM policy
	ctx := context.Background()
	grant, err := m.getUserGrantFromIAM(ctx, email)
	if err != nil {
		return nil, err
	}

	// Update cache if grant found
	if grant != nil {
		m.mutex.Lock()
		m.grantsCache[email] = grant
		m.mutex.Unlock()
	}

	return grant, nil
}

// extractServiceAccountEmail extracts the email from a service account member string
// Member format: serviceAccount:sa@project.iam.gserviceaccount.com
func extractServiceAccountEmail(member string) string {
	prefix := "serviceAccount:"
	if strings.HasPrefix(member, prefix) {
		return member[len(prefix):]
	}
	return ""
}

// sanitizeUsernameReversible encodes email username for use in service account names
// Assumes email domain is @redhat.com (constant)
// Special characters are encoded to make the transformation reversible:
//
//	. → -dot-
//	- → -dash-
//
// Example: user.name@redhat.com → user-dot-name
func sanitizeUsernameReversible(email string) string {
	// Extract username (part before @)
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		// Fallback: treat entire string as username
		klog.Warningf("Email %s does not contain @, treating as username", email)
		username := email
		username = strings.ToLower(username)
		username = strings.ReplaceAll(username, "-", "-dash-")
		username = strings.ReplaceAll(username, ".", "-dot-")
		return username
	}

	username := parts[0]
	domain := parts[1]

	// Validate domain
	if strings.ToLower(domain) != "redhat.com" {
		klog.Warningf("Email %s is not @redhat.com, storing full email in DisplayName", email)
	}

	// Encode special characters
	// Order matters: escape existing hyphens first
	username = strings.ToLower(username)
	username = strings.ReplaceAll(username, "-", "-dash-")
	username = strings.ReplaceAll(username, ".", "-dot-")

	return username
}

// unsanitizeUsername reverses the sanitization to recover original username
// Appends @redhat.com domain to create full email
// Example: user-dot-name → user.name@redhat.com
func unsanitizeUsername(sanitized string) string {
	username := sanitized
	// Reverse order: decode in opposite sequence
	username = strings.ReplaceAll(username, "-dot-", ".")
	username = strings.ReplaceAll(username, "-dash-", "-")

	// Append redhat.com domain
	return username + "@redhat.com"
}

// needsDisplayNameFallback checks if an email would require DisplayName fallback
// during service account creation (i.e., the sanitized username would be too long)
func needsDisplayNameFallback(email string) bool {
	sanitized := sanitizeUsernameReversible(email)
	// GCP service account names must be 6-30 characters
	// Format: {sanitized-username}-{timestamp}
	// Reserve 11 characters for dash and 10-digit timestamp
	maxUsernameLength := 30 - 11
	return len(sanitized) > maxUsernameLength
}

// extractUserEmailFromServiceAccount extracts the user email from a service account email
// Service account format: {sanitized-username}-{timestamp}@{project}.iam.gserviceaccount.com
// Example: user-dot-name-1234567890@project.iam.gserviceaccount.com → user.name@redhat.com
func extractUserEmailFromServiceAccount(serviceAccountEmail string) string {
	// Extract the account ID part (before @)
	parts := strings.Split(serviceAccountEmail, "@")
	if len(parts) != 2 {
		klog.V(2).Infof("Invalid service account email format: %s", serviceAccountEmail)
		return ""
	}

	accountID := parts[0]

	// Remove timestamp suffix (last 11 characters: -1234567890)
	// Service account format: {sanitized-username}-{10-digit-timestamp}
	if len(accountID) < 11 {
		klog.V(2).Infof("Account ID too short: %s", accountID)
		return ""
	}

	// Find the last hyphen that separates username from timestamp
	// The timestamp is always a 10-digit number
	lastHyphenIdx := strings.LastIndex(accountID, "-")
	if lastHyphenIdx == -1 {
		klog.V(2).Infof("No hyphen found in account ID: %s", accountID)
		return ""
	}

	// Verify the suffix is a timestamp (10 digits)
	timestampPart := accountID[lastHyphenIdx+1:]
	if len(timestampPart) != 10 {
		klog.V(2).Infof("Timestamp part is not 10 digits: %s", timestampPart)
		return ""
	}

	// Verify it's all digits
	for _, c := range timestampPart {
		if c < '0' || c > '9' {
			klog.V(2).Infof("Timestamp contains non-digit: %s", timestampPart)
			return ""
		}
	}

	// Extract sanitized username (everything before timestamp)
	sanitizedUsername := accountID[:lastHyphenIdx]

	// Unsanitize to get original email (appends @redhat.com)
	return unsanitizeUsername(sanitizedUsername)
}

// parseExpirationFromCondition extracts the expiration timestamp from an IAM condition expression
// Expression format: request.time < timestamp('2026-01-19T12:34:56Z')
func parseExpirationFromCondition(condition *cloudresourcemanager.Expr) (time.Time, error) {
	if condition == nil || condition.Expression == "" {
		return time.Time{}, fmt.Errorf("condition or expression is empty")
	}

	matches := timestampRegex.FindStringSubmatch(condition.Expression)
	if len(matches) < 2 {
		return time.Time{}, fmt.Errorf("failed to extract timestamp from expression: %s", condition.Expression)
	}

	timestamp := matches[1]
	expiresAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse timestamp %s: %w", timestamp, err)
	}

	return expiresAt, nil
}

// extractEmailFromMember extracts the email from a member string
// Member format: user:email@example.com
func extractEmailFromMember(member string) string {
	if len(member) > 5 && member[:5] == "user:" {
		return member[5:]
	}
	return ""
}

// isAlreadyMemberError checks if the error indicates the user is already a member
func isAlreadyMemberError(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		// HTTP 409 Conflict is returned when the member already exists
		return apiErr.Code == http.StatusConflict
	}
	return false
}

// isNotFoundError checks if the error indicates the resource was not found
func isNotFoundError(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusNotFound
	}
	return false
}
