package gsmsecrets

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/iam/apiv1/iampb"

	"github.com/openshift/ci-tools/pkg/gsm-validation"
)

const (
	TestPlatform = "test platform"

	GCPMaxServiceAccountIDLength = 30

	UpdaterSASecretName   = "updater-service-account"
	UpdaterSASecretSuffix = "__updater-service-account"
	IndexSecretSuffix     = "____index"

	ServiceAccountIDSuffix          = "-updater"
	ServiceAccountDescriptionPrefix = "Updater service account for secret collection: "

	// IAM binding condition title prefixes
	SecretsViewerConditionTitlePrefix  = "Read access to secrets for "
	SecretsUpdaterConditionTitlePrefix = "Create, update, and delete access for "

	// IAM binding condition description templates
	SecretsViewerConditionDescriptionTemplate  = "Managed by %s: Read access to secrets in %s collection"
	SecretsUpdaterConditionDescriptionTemplate = "Managed by %s: Create, update, and delete access to secrets in %s collection"
)

type Config struct {
	ProjectIdString string `json:"GCP_PROJECT_ID" yaml:"GCP_PROJECT_ID"`
	ProjectIdNumber string `json:"GCP_PROJECT_NUMBER" yaml:"GCP_PROJECT_NUMBER"`
}

func (c Config) GetSecretAccessorRole() string {
	return fmt.Sprintf("projects/%s/roles/openshift_ci_secrets_viewer", c.ProjectIdString)
}

func (c Config) GetSecretUpdaterRole() string {
	return fmt.Sprintf("projects/%s/roles/openshift_ci_secrets_updater", c.ProjectIdString)
}

// DesiredGroupsMap represents the groups contained within the _config.yaml file.
type DesiredGroupsMap map[string]GroupAccessInfo
type SAMap map[string]ServiceAccountInfo

type GroupAccessInfo struct {
	Name              string
	Email             string
	SecretCollections []string
}

type DesiredCollection struct {
	Name             string
	GroupsWithAccess []string
}

// SecretType represents the type of secret for cleanup decisions
type SecretType int

const (
	SecretTypeUnknown SecretType = iota
	SecretTypeSA                 // Service Account secrets
	SecretTypeIndex              // Index secrets
	SecretTypeGeneric            // Generic secrets
)

type GCPSecret struct {
	Name         string // just the name, e.g. "my-secret"
	ResourceName string // full resource name, e.g. "projects/openshift-ci-secrets/secrets/my-secret"
	Collection   string
	Labels       map[string]string
	Annotations  map[string]string
	Payload      []byte
	Type         SecretType // Classification for cleanup decisions
}

// CanonicalIAMBinding is a simplified, canonical representation for diffing IAM bindings.
type CanonicalIAMBinding struct {
	Role           string
	Members        string // Sorted members joined by a delimiter (e.g., ",")
	ConditionTitle string // The condition title, or "" if no condition
	ConditionDesc  string // The condition description, or "" if no condition
	ConditionExpr  string // The raw expression string, or "" if no condition
}

// ServiceAccountInfo represents the actual state of an updater Service Account in GCP
type ServiceAccountInfo struct {
	Email       string
	DisplayName string
	ID          string
	Collection  string
	Description string
}

type Actions struct {
	Config                Config
	SAsToCreate           SAMap
	SAsToDelete           SAMap
	SecretsToCreate       map[string]GCPSecret
	SecretsToDelete       []GCPSecret
	ConsolidatedIAMPolicy *iampb.Policy
}

func GetConfigFromEnv() (Config, error) {
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		return Config{}, fmt.Errorf("GCP_PROJECT_ID environment variable is required")
	}

	projectNumber := os.Getenv("GCP_PROJECT_NUMBER")
	if projectNumber == "" {
		return Config{}, fmt.Errorf("GCP_PROJECT_NUMBER environment variable is required")
	}

	return Config{
		ProjectIdString: projectID,
		ProjectIdNumber: projectNumber,
	}, nil
}

// GetProjectResourceIdNumber returns the resource id number for our GCP project
// in format `projects/{project id number}`, e.g., "projects/1234567890"
func GetProjectResourceIdNumber(projectIdNumber string) string {
	return fmt.Sprintf("projects/%s", projectIdNumber)
}

// GetProjectResourceString returns the resource string for our GCP project
// in format `projects/{project id string}`, e.g., "projects/ci-secrets"
func GetProjectResourceString(projectIdString string) string {
	return fmt.Sprintf("projects/%s", projectIdString)
}

// GetUpdaterSAEmailRegex returns the regex pattern for updater service account emails for a given project
func GetUpdaterSAEmailRegex(config Config) string {
	return fmt.Sprintf(`[a-z0-9-]+%s@%s\.iam\.gserviceaccount\.com$`, ServiceAccountIDSuffix, config.ProjectIdString)
}

// GetUpdaterSAEmail returns the updater service account email for a collection.
func GetUpdaterSAEmail(collection string, config Config) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", GetUpdaterSAId(collection), config.ProjectIdString)
}

// GetUpdaterSAId returns the updater service account ID for a given collection name.
// Uses the collection name directly if it fits within GCP's 30-character limit,
// otherwise uses a hash-based approach.
func GetUpdaterSAId(collection string) string {
	suffixLen := len(ServiceAccountIDSuffix)
	directId := fmt.Sprintf("%s%s", collection, ServiceAccountIDSuffix)

	if len(directId) <= GCPMaxServiceAccountIDLength {
		return directId
	}

	maxHashLen := GCPMaxServiceAccountIDLength - suffixLen
	hash := sha256.Sum256([]byte(collection))
	encodedHash := base64.RawURLEncoding.EncodeToString(hash[:])

	if len(encodedHash) > maxHashLen {
		encodedHash = encodedHash[:maxHashLen]
	}

	return fmt.Sprintf("%s%s", strings.ToLower(encodedHash), ServiceAccountIDSuffix)
}

// GetUpdaterSADisplayName returns the display name for the service account,
// which is the collection name.
func GetUpdaterSADisplayName(collection string) string {
	return collection
}

// GetUpdaterSADescription returns the description for the service account
func GetUpdaterSADescription(collection string) string {
	return fmt.Sprintf("%s%s", ServiceAccountDescriptionPrefix, collection)
}

// ExtractCollectionFromDescription extracts the collection name from a service account description
func ExtractCollectionFromDescription(description string) string {
	if after, ok := strings.CutPrefix(description, ServiceAccountDescriptionPrefix); ok {
		return after
	}
	return ""
}

// GetUpdaterSASecretName returns standardized name for updater service account secret,
// `{collection}__updater-service-account`.
func GetUpdaterSASecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, UpdaterSASecretSuffix)
}

// GetIndexSecretName returns standardized name for the index secret,
// `{collection}____index`.
func GetIndexSecretName(collection string) string {
	return fmt.Sprintf("%s%s", collection, IndexSecretSuffix)
}

// GetSecretID extracts the secret ID from the secret name, e.g.,
// "projects/openshift-ci-secrets/secrets/collection__secret" -> "collection__secret"
func GetSecretID(secretName string) string {
	parts := strings.Split(secretName, "/")
	return parts[len(parts)-1] // Extract just the secret ID
}

// GetGSMSecretName returns the actual secret name in GSM, in format {collection}__{group}__{secret}
// Group path separators (/) are converted to __ for the hierarchical structure.
//
// Example: collection="vsphere", group="ibmcloud/ci", field="username" => "vsphere__ibmcloud__ci__username"
func GetGSMSecretName(collection, group, field string) string {
	// Convert group path separators (/) to __
	groupParts := strings.ReplaceAll(group, "/", gsmvalidation.CollectionSecretDelimiter)

	// Build hierarchical name: collection__group__field
	if groupParts == "" {
		return fmt.Sprintf("%s%s%s", collection, gsmvalidation.CollectionSecretDelimiter, field)
	}
	return fmt.Sprintf("%s%s%s%s%s", collection, gsmvalidation.CollectionSecretDelimiter, groupParts, gsmvalidation.CollectionSecretDelimiter, field)
}

// GetGSMSecretResourceName returns the full GCP resource name for a GSM secret,
// in format: "projects/{project ID number}/secrets/{collection}__{group}__{secret}"
func GetGSMSecretResourceName(projectIdNumber, collection, group, field string) string {
	return fmt.Sprintf("%s/secrets/%s",
		GetProjectResourceIdNumber(projectIdNumber),
		GetGSMSecretName(collection, group, field))
}
