package manager

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

// newTestManager creates a GCPAccessManager for testing in dry-run mode
// This avoids duplication of manager setup across multiple tests
func newTestManager() *GCPAccessManager {
	return &GCPAccessManager{
		projectID:   "test-project",
		iamRole:     "roles/viewer",
		enabled:     true,
		bqEnabled:   false,
		dryRun:      true,
		grantsCache: make(map[string]*UserAccessGrant),
	}
}

// ============================================================================
// Manager Initialization Tests
// ============================================================================

func TestNewGCPAccessManager_Disabled(t *testing.T) {
	t.Parallel()

	manager, err := NewGCPAccessManager("", false)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if manager == nil {
		t.Error("Expected manager to be created")
	}
	if manager.IsEnabled() {
		t.Error("Expected manager to be disabled")
	}
}

// ============================================================================
// Error Helper Tests
// ============================================================================

// TestErrorHelpers tests both isAlreadyMemberError and isNotFoundError functions
func TestErrorHelpers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		err                 error
		expectAlreadyMember bool
		expectNotFound      bool
	}{
		{
			name: "409 Conflict error",
			err: &googleapi.Error{
				Code: http.StatusConflict,
			},
			expectAlreadyMember: true,
			expectNotFound:      false,
		},
		{
			name: "404 Not Found error",
			err: &googleapi.Error{
				Code: http.StatusNotFound,
			},
			expectAlreadyMember: false,
			expectNotFound:      true,
		},
		{
			name: "500 Internal Server error",
			err: &googleapi.Error{
				Code: http.StatusInternalServerError,
			},
			expectAlreadyMember: false,
			expectNotFound:      false,
		},
		{
			name:                "Generic error",
			err:                 errors.New("generic error"),
			expectAlreadyMember: false,
			expectNotFound:      false,
		},
		{
			name:                "Nil error",
			err:                 nil,
			expectAlreadyMember: false,
			expectNotFound:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if result := isAlreadyMemberError(tc.err); result != tc.expectAlreadyMember {
				t.Errorf("isAlreadyMemberError: expected %v, got %v", tc.expectAlreadyMember, result)
			}
			if result := isNotFoundError(tc.err); result != tc.expectNotFound {
				t.Errorf("isNotFoundError: expected %v, got %v", tc.expectNotFound, result)
			}
		})
	}
}

// ============================================================================
// Operations When Disabled Tests
// ============================================================================

func TestGCPAccessManager_OperationsWhenDisabled(t *testing.T) {
	t.Parallel()

	// Create a disabled manager
	manager, _ := NewGCPAccessManager("", false)

	testCases := []struct {
		name        string
		op          func() error
		expectError bool
	}{
		{
			name: "GrantAccess",
			op: func() error {
				_, _, err := manager.GrantAccess("user@example.com", "U12345", "Testing", "gcp")
				return err
			},
			expectError: true,
		},
		{
			name: "RevokeAccess",
			op: func() error {
				return manager.RevokeAccess("user@example.com")
			},
			expectError: true,
		},
		{
			name: "GetUserGrant",
			op: func() error {
				_, err := manager.GetUserGrant("user@example.com")
				return err
			},
			expectError: true,
		},
		{
			name: "CleanupExpiredAccess",
			op: func() error {
				return manager.CleanupExpiredAccess()
			},
			expectError: false, // CleanupExpiredAccess should not error when disabled, just no-op
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.op()
			if tc.expectError && err == nil {
				t.Error("Expected error when manager is disabled")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error when manager is disabled, got: %v", err)
			}
		})
	}
}

// ============================================================================
// IAM Policy Parsing Helper Tests
// ============================================================================

func TestParseExpirationFromCondition(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		condition   *cloudresourcemanager.Expr
		expectError bool
		expected    string // RFC3339 format
	}{
		{
			name: "Valid condition",
			condition: &cloudresourcemanager.Expr{
				Expression: "request.time < timestamp('2026-01-19T12:34:56Z')",
			},
			expectError: false,
			expected:    "2026-01-19T12:34:56Z",
		},
		{
			name:        "Nil condition",
			condition:   nil,
			expectError: true,
		},
		{
			name: "Empty expression",
			condition: &cloudresourcemanager.Expr{
				Expression: "",
			},
			expectError: true,
		},
		{
			name: "Invalid expression format",
			condition: &cloudresourcemanager.Expr{
				Expression: "invalid expression",
			},
			expectError: true,
		},
		{
			name: "Invalid timestamp format",
			condition: &cloudresourcemanager.Expr{
				Expression: "request.time < timestamp('not-a-timestamp')",
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseExpirationFromCondition(tc.condition)
			if tc.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				expectedTime, _ := time.Parse(time.RFC3339, tc.expected)
				if !result.Equal(expectedTime) {
					t.Errorf("Expected %v, got %v", expectedTime, result)
				}
			}
		})
	}
}

func TestExtractEmailFromMember(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Valid user member",
			input:    "user:test@example.com",
			expected: "test@example.com",
		},
		{
			name:     "Invalid prefix",
			input:    "group:test@example.com",
			expected: "",
		},
		{
			name:     "No prefix",
			input:    "test@example.com",
			expected: "",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Just user prefix",
			input:    "user:",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractEmailFromMember(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestExtractServiceAccountEmail(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Valid service account member",
			input:    "serviceAccount:test-sa@project.iam.gserviceaccount.com",
			expected: "test-sa@project.iam.gserviceaccount.com",
		},
		{
			name:     "User member (wrong prefix)",
			input:    "user:user@example.com",
			expected: "",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Service account prefix only",
			input:    "serviceAccount:",
			expected: "",
		},
		{
			name:     "No prefix",
			input:    "test-sa@project.iam.gserviceaccount.com",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractServiceAccountEmail(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// ============================================================================
// Core Grant/Revoke Operations Tests
// ============================================================================

// TestGrantAccess tests the grant access lifecycle in dry-run mode
// Focuses on grant state transitions: initial grant, re-grant, and expired grant replacement
func TestGrantAccess(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		setupCache     func(*GCPAccessManager)
		email          string
		slackID        string
		justification  string
		resource       string
		validateResult func(*testing.T, []byte, time.Time, error, *GCPAccessManager)
	}{
		{
			name:          "Initial grant",
			setupCache:    func(m *GCPAccessManager) {},
			email:         "newuser@example.com",
			slackID:       "U12345",
			justification: "Initial access request",
			resource:      "gcp",
			validateResult: func(t *testing.T, keyJSON []byte, expiresAt time.Time, err error, m *GCPAccessManager) {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				if len(keyJSON) == 0 {
					t.Error("Expected key JSON to be returned")
				}
				if expiresAt.IsZero() {
					t.Error("Expected valid expiration time")
				}
				// Verify cache was updated with all fields
				grant, exists := m.grantsCache["newuser@example.com"]
				if !exists {
					t.Fatal("Expected cache to be updated")
				}
				if grant.Email != "newuser@example.com" {
					t.Errorf("Expected email newuser@example.com, got %s", grant.Email)
				}
				if grant.Justification != "Initial access request" {
					t.Errorf("Expected justification 'Initial access request', got %s", grant.Justification)
				}
				if grant.RequestedBy != "U12345" {
					t.Errorf("Expected RequestedBy U12345, got %s", grant.RequestedBy)
				}
				if grant.ServiceAccountEmail == "" {
					t.Error("Expected ServiceAccountEmail to be populated")
				}
				// Verify timestamps are reasonable
				now := time.Now()
				if grant.GrantedAt.After(now) {
					t.Error("GrantedAt should not be in the future")
				}
				if grant.GrantedAt.Before(now.Add(-1 * time.Minute)) {
					t.Error("GrantedAt should be recent")
				}
				expectedExpiration := grant.GrantedAt.Add(GCPAccessDuration)
				if !grant.ExpiresAt.Equal(expectedExpiration) {
					t.Errorf("Expected ExpiresAt %v, got %v", expectedExpiration, grant.ExpiresAt)
				}
			},
		},
		{
			name: "Re-grant with active access (key regeneration)",
			setupCache: func(m *GCPAccessManager) {
				m.grantsCache["existing@example.com"] = &UserAccessGrant{
					Email:               "existing@example.com",
					GrantedAt:           time.Now().Add(-1 * time.Hour),
					ExpiresAt:           time.Now().Add(6 * 24 * time.Hour),
					ServiceAccountEmail: "existing-sa@test-project.iam.gserviceaccount.com",
					RequestedBy:         "U11111",
					Justification:       "Original grant",
				}
			},
			email:         "existing@example.com",
			slackID:       "U11111",
			justification: "Re-grant request",
			resource:      "gcp",
			validateResult: func(t *testing.T, keyJSON []byte, expiresAt time.Time, err error, m *GCPAccessManager) {
				if err != nil {
					t.Errorf("Expected no error for re-grant, got: %v", err)
				}
				if keyJSON == nil {
					t.Error("Expected key JSON from re-grant")
				}
				// Expiration should remain the same (from original grant)
				grant := m.grantsCache["existing@example.com"]
				if !grant.ExpiresAt.Equal(expiresAt) {
					t.Error("Expiration time should match original grant")
				}
			},
		},
		{
			name: "Replace expired grant",
			setupCache: func(m *GCPAccessManager) {
				m.grantsCache["expired@example.com"] = &UserAccessGrant{
					Email:         "expired@example.com",
					GrantedAt:     time.Now().Add(-8 * 24 * time.Hour),
					ExpiresAt:     time.Now().Add(-24 * time.Hour), // Expired 1 day ago
					RequestedBy:   "U22222",
					Justification: "Old expired grant",
				}
			},
			email:         "expired@example.com",
			slackID:       "U22222",
			justification: "New grant after expiration",
			resource:      "gcp",
			validateResult: func(t *testing.T, keyJSON []byte, expiresAt time.Time, err error, m *GCPAccessManager) {
				if err != nil {
					t.Errorf("Expected grant to succeed for expired access, got: %v", err)
				}
				grant := m.grantsCache["expired@example.com"]
				if grant.Justification != "New grant after expiration" {
					t.Errorf("Expected justification 'New grant after expiration', got %s", grant.Justification)
				}
				if grant.ExpiresAt.Before(time.Now()) {
					t.Error("Expected new grant to have future expiration")
				}
				// Verify new grant has proper duration
				expectedExpiration := grant.GrantedAt.Add(GCPAccessDuration)
				if !grant.ExpiresAt.Equal(expectedExpiration) {
					t.Errorf("Expected expiration %v, got %v", expectedExpiration, grant.ExpiresAt)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := newTestManager()
			tc.setupCache(manager)
			keyJSON, expiresAt, err := manager.GrantAccess(tc.email, tc.slackID, tc.justification, tc.resource)
			tc.validateResult(t, keyJSON, expiresAt, err, manager)
		})
	}
}

// TestRevokeAccess tests revocation in dry-run mode
func TestRevokeAccess(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		setupCache func(*GCPAccessManager)
		email      string
		expectErr  bool
		validate   func(*testing.T, *GCPAccessManager)
	}{
		{
			name: "Revoke existing grant",
			setupCache: func(m *GCPAccessManager) {
				m.grantsCache["test@example.com"] = &UserAccessGrant{
					Email:               "test@example.com",
					GrantedAt:           time.Now(),
					ExpiresAt:           time.Now().Add(7 * 24 * time.Hour),
					ServiceAccountEmail: "test-sa@project.iam.gserviceaccount.com",
					RequestedBy:         "U12345",
					Justification:       "Testing",
				}
			},
			email:     "test@example.com",
			expectErr: false,
			validate: func(t *testing.T, m *GCPAccessManager) {
				if _, exists := m.grantsCache["test@example.com"]; exists {
					t.Error("Expected cache to be cleared after revocation")
				}
			},
		},
		{
			name:       "Revoke non-existent grant (empty cache)",
			setupCache: func(m *GCPAccessManager) {},
			email:      "nonexistent@example.com",
			expectErr:  false,
			validate: func(t *testing.T, m *GCPAccessManager) {
				if len(m.grantsCache) != 0 {
					t.Errorf("Expected empty cache, got %d entries", len(m.grantsCache))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := newTestManager()
			tc.setupCache(manager)
			err := manager.RevokeAccess(tc.email)
			if tc.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tc.expectErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
			tc.validate(t, manager)
		})
	}
}

// ============================================================================
// Cache Operations Tests
// ============================================================================

// TestCacheOperations tests cache-specific behaviors beyond basic grant/revoke
func TestCacheOperations(t *testing.T) {
	t.Parallel()

	t.Run("GetUserGrant returns cached grant", func(t *testing.T) {
		manager := newTestManager()
		expectedGrant := &UserAccessGrant{
			Email:         "cached@example.com",
			GrantedAt:     time.Now().Add(-1 * time.Hour),
			ExpiresAt:     time.Now().Add(6 * 24 * time.Hour),
			RequestedBy:   "U99999",
			Justification: "Testing cache hit",
		}
		manager.grantsCache["cached@example.com"] = expectedGrant

		// Should return cached value without calling GCP API (crmService is nil)
		grant, err := manager.GetUserGrant("cached@example.com")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if grant == nil {
			t.Fatal("Expected grant to be returned")
		}
		if grant.Email != expectedGrant.Email {
			t.Errorf("Expected email %s, got %s", expectedGrant.Email, grant.Email)
		}
		if grant.RequestedBy != expectedGrant.RequestedBy {
			t.Errorf("Expected RequestedBy %s, got %s", expectedGrant.RequestedBy, grant.RequestedBy)
		}
	})

	t.Run("Multiple users can have active grants", func(t *testing.T) {
		manager := newTestManager()

		users := []struct {
			email         string
			slackID       string
			justification string
		}{
			{"user1@example.com", "U11111", "User 1 justification"},
			{"user2@example.com", "U22222", "User 2 justification"},
			{"user3@example.com", "U33333", "User 3 justification"},
		}

		// Grant to all users
		for _, user := range users {
			_, _, err := manager.GrantAccess(user.email, user.slackID, user.justification, "gcp")
			if err != nil {
				t.Errorf("Failed to grant to %s: %v", user.email, err)
			}
		}

		// Verify all are in cache with correct data
		if len(manager.grantsCache) != len(users) {
			t.Errorf("Expected %d grants in cache, got %d", len(users), len(manager.grantsCache))
		}

		for _, user := range users {
			grant, exists := manager.grantsCache[user.email]
			if !exists {
				t.Errorf("Expected grant for %s in cache", user.email)
				continue
			}
			if grant.RequestedBy != user.slackID {
				t.Errorf("Expected RequestedBy %s, got %s", user.slackID, grant.RequestedBy)
			}
			if grant.Justification != user.justification {
				t.Errorf("Expected justification %s, got %s", user.justification, grant.Justification)
			}
		}
	})
}

// TestGetUserGrant_CacheMiss tests GetUserGrant when user is not in cache
// In dry-run mode with nil crmService, this should return nil without error
func TestGetUserGrant_CacheMiss(t *testing.T) {
	t.Parallel()

	manager := newTestManager()

	// Query for non-existent user
	grant, err := manager.GetUserGrant("nonexistent@example.com")

	// With nil crmService (dry-run setup), getUserGrantFromIAM returns nil, nil
	if err != nil {
		t.Errorf("Expected no error for cache miss with nil crmService, got: %v", err)
	}
	if grant != nil {
		t.Error("Expected nil grant for non-existent user")
	}
}

// ============================================================================
// Username/Email Extraction and Sanitization Helper Tests
// ============================================================================

// TestSanitizeUsernameReversible tests the username sanitization function
func TestSanitizeUsernameReversible(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		email    string
		expected string
	}{
		{
			name:     "Simple email",
			email:    "jsmith@redhat.com",
			expected: "jsmith",
		},
		{
			name:     "Email with dot",
			email:    "jane.doe@redhat.com",
			expected: "jane-dot-doe",
		},
		{
			name:     "Email with hyphen",
			email:    "john-smith@redhat.com",
			expected: "john-dash-smith",
		},
		{
			name:     "Complex username",
			email:    "test.user-admin@redhat.com",
			expected: "test-dot-user-dash-admin",
		},
		{
			name:     "Non-redhat domain (warning case)",
			email:    "user@example.com",
			expected: "user",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeUsernameReversible(tc.email)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// TestUnsanitizeUsername tests the username unsanitization function
func TestUnsanitizeUsername(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		sanitized string
		expected  string
	}{
		{
			name:      "Simple username",
			sanitized: "jsmith",
			expected:  "jsmith@redhat.com",
		},
		{
			name:      "Username with dot",
			sanitized: "jane-dot-doe",
			expected:  "jane.doe@redhat.com",
		},
		{
			name:      "Username with hyphen",
			sanitized: "john-dash-smith",
			expected:  "john-smith@redhat.com",
		},
		{
			name:      "Complex username",
			sanitized: "test-dot-user-dash-admin",
			expected:  "test.user-admin@redhat.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := unsanitizeUsername(tc.sanitized)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// TestNeedsDisplayNameFallback tests the needsDisplayNameFallback function
func TestNeedsDisplayNameFallback(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		email    string
		expected bool
	}{
		{
			name:     "Short email - no fallback needed",
			email:    "user@redhat.com",
			expected: false,
		},
		{
			name:     "Medium email - no fallback needed",
			email:    "john.smith@redhat.com",
			expected: false,
		},
		{
			name:     "Long email - fallback needed",
			email:    "very.long.username.here@redhat.com",
			expected: true,
		},
		{
			name: "Email at boundary (19 chars after sanitization) - no fallback",
			// "test-dot-user" = 13 chars + "-dot-" (5) = 18, still under 19
			email:    "test.user@redhat.com",
			expected: false,
		},
		{
			name: "Email just over boundary - fallback needed",
			// This will create a sanitized name > 19 characters
			email:    "firstname.middlename.lastname@redhat.com",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := needsDisplayNameFallback(tc.email)
			if result != tc.expected {
				sanitized := sanitizeUsernameReversible(tc.email)
				t.Errorf("Expected %v for %q (sanitized to %q, length %d), got %v",
					tc.expected, tc.email, sanitized, len(sanitized), result)
			}
		})
	}
}

// TestExtractUserEmailFromServiceAccount tests extracting user email from SA email
func TestExtractUserEmailFromServiceAccount(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		serviceAccountEmail string
		expected            string
	}{
		{
			name:                "Simple username",
			serviceAccountEmail: "jsmith-1234567890@project.iam.gserviceaccount.com",
			expected:            "jsmith@redhat.com",
		},
		{
			name:                "Username with dot",
			serviceAccountEmail: "jane-dot-doe-1234567890@project.iam.gserviceaccount.com",
			expected:            "jane.doe@redhat.com",
		},
		{
			name:                "Username with hyphen",
			serviceAccountEmail: "john-dash-smith-1234567890@project.iam.gserviceaccount.com",
			expected:            "john-smith@redhat.com",
		},
		{
			name:                "Complex username",
			serviceAccountEmail: "test-dot-user-dash-admin-1234567890@project.iam.gserviceaccount.com",
			expected:            "test.user-admin@redhat.com",
		},
		{
			name:                "Invalid format (no @)",
			serviceAccountEmail: "jsmith-1234567890",
			expected:            "",
		},
		{
			name:                "Invalid timestamp (not 10 digits)",
			serviceAccountEmail: "jsmith-123@project.iam.gserviceaccount.com",
			expected:            "",
		},
		{
			name:                "Invalid timestamp (contains letters)",
			serviceAccountEmail: "jsmith-123456789a@project.iam.gserviceaccount.com",
			expected:            "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractUserEmailFromServiceAccount(tc.serviceAccountEmail)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

// ============================================================================
// CleanupExpiredAccess Tests
// ============================================================================

// TestCleanupExpiredAccess_Disabled verifies behavior when manager is disabled
func TestCleanupExpiredAccess_Disabled(t *testing.T) {
	t.Parallel()

	manager, _ := NewGCPAccessManager("", false)

	// Should not error when disabled, just no-op
	err := manager.CleanupExpiredAccess()
	if err != nil {
		t.Errorf("Expected no error when disabled, got: %v", err)
	}
}

// NOTE: Full testing of CleanupExpiredAccess requires either:
// 1. A mock CRM service implementation
// 2. Integration tests against a real GCP test project
//
// The current dry-run mode setup (newTestManager with nil crmService) cannot
// test the full cleanup logic since CleanupExpiredAccess calls
// m.crmService.Projects.GetIamPolicy(), which would panic with nil crmService.
//
// Future improvement: Add mock CRM service to enable comprehensive unit testing
// of the cleanup logic including:
// - Expired binding removal
// - Service account deletion
// - Cache cleanup
// - Error handling

// ============================================================================
// Concurrency and Edge Case Tests
// ============================================================================

// TestConcurrentAccess tests thread safety of grant/revoke operations
func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	manager := newTestManager()

	// Run multiple concurrent grant operations
	const numGoroutines = 10
	grantDone := make(chan bool, numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer func() { grantDone <- true }()
			email := fmt.Sprintf("user%d@example.com", id)
			slackID := fmt.Sprintf("U%d", id)
			_, _, err := manager.GrantAccess(email, slackID, "Concurrent test", "gcp")
			if err != nil {
				t.Errorf("Concurrent grant failed for %s: %v", email, err)
			}
		}(i)
	}

	// Wait for all grant operations to complete
	for range numGoroutines {
		<-grantDone
	}

	// Verify all grants are in cache
	if len(manager.grantsCache) != numGoroutines {
		t.Errorf("Expected %d grants in cache, got %d", numGoroutines, len(manager.grantsCache))
	}

	// Now test concurrent revoke with separate channel
	revokeDone := make(chan bool, numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			defer func() { revokeDone <- true }()
			email := fmt.Sprintf("user%d@example.com", id)
			err := manager.RevokeAccess(email)
			if err != nil {
				t.Errorf("Concurrent revoke failed for %s: %v", email, err)
			}
		}(i)
	}

	// Wait for all revoke operations
	for range numGoroutines {
		<-revokeDone
	}

	// Cache should be empty
	if len(manager.grantsCache) != 0 {
		t.Errorf("Expected empty cache after concurrent revokes, got %d entries", len(manager.grantsCache))
	}
}

// TestEdgeCases tests edge cases for email handling
func TestEdgeCases(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		email         string
		expectSuccess bool
	}{
		{
			name:          "Very long email (50+ chars)",
			email:         "very.long.email.address.with.many.dots@redhat.com",
			expectSuccess: true,
		},
		{
			name:          "Single character username",
			email:         "a@redhat.com",
			expectSuccess: true,
		},
		{
			name:          "Username with multiple dots",
			email:         "first.middle.last@redhat.com",
			expectSuccess: true,
		},
		{
			name:          "Username with multiple hyphens",
			email:         "first-middle-last@redhat.com",
			expectSuccess: true,
		},
		{
			name:          "Mixed dots and hyphens",
			email:         "first.last-name@redhat.com",
			expectSuccess: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := newTestManager()
			_, _, err := manager.GrantAccess(tc.email, "U12345", "Edge case test", "gcp")

			if tc.expectSuccess && err != nil {
				t.Errorf("Expected success for %s, got error: %v", tc.email, err)
			}
			if !tc.expectSuccess && err == nil {
				t.Errorf("Expected error for %s, but got success", tc.email)
			}

			if tc.expectSuccess {
				// Verify cache was updated
				if _, exists := manager.grantsCache[tc.email]; !exists {
					t.Errorf("Expected %s in cache", tc.email)
				}
			}
		})
	}
}
