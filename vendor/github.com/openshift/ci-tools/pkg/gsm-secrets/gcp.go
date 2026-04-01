package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"cloud.google.com/go/iam/admin/apiv1/adminpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

func GetProjectIAMPolicy(ctx context.Context, client ResourceManagerClient, projectIdNumber string) (*iampb.Policy, error) {
	policyRequest := &iampb.GetIamPolicyRequest{
		Resource: GetProjectResourceIdNumber(projectIdNumber),
		Options: &iampb.GetPolicyOptions{
			RequestedPolicyVersion: 3, // necessary for IAM Conditions support
		},
	}

	policy, err := client.GetIamPolicy(ctx, policyRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to get IAM policy for project: %w", err)
	}

	return policy, nil
}

func GetUpdaterServiceAccounts(ctx context.Context, client IAMClient, config Config) ([]ServiceAccountInfo, error) {
	var actualServiceAccounts []ServiceAccountInfo

	accountIterator := client.ListServiceAccounts(ctx, &adminpb.ListServiceAccountsRequest{Name: GetProjectResourceString(config.ProjectIdString)})
	for {
		serviceAccount, err := accountIterator.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		// Only add updater service accounts
		if matched, _ := regexp.MatchString(GetUpdaterSAEmailRegex(config), serviceAccount.Email); matched {
			collection := serviceAccount.DisplayName // The display name is the collection name
			if collection == "" {
				// Fallback: try to extract from description
				collection = ExtractCollectionFromDescription(serviceAccount.Description)
			}
			if collection == "" {
				return nil, fmt.Errorf("couldn't determine collection of service account %s", serviceAccount.Email)
			}

			emailParts := strings.Split(serviceAccount.Email, "@")
			id := ""
			if len(emailParts) > 0 {
				id = emailParts[0]
			}

			actualServiceAccounts = append(actualServiceAccounts, ServiceAccountInfo{
				Email:       serviceAccount.Email,
				DisplayName: serviceAccount.DisplayName,
				ID:          id,
				Collection:  collection,
				Description: serviceAccount.Description,
			})
		}
	}
	return actualServiceAccounts, nil
}

// GetAllSecrets returns all secrets in the gcp project, as a map of secret IDs to secrets
func GetAllSecrets(ctx context.Context, client SecretManagerClient, config Config) (map[string]GCPSecret, error) {
	it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: GetProjectResourceString(config.ProjectIdString),
	})

	actualSecrets := make(map[string]GCPSecret)
	for {
		secret, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		secretID := GetSecretID(secret.Name)
		collection := ExtractCollectionFromSecretName(secretID)
		if collection == "" {
			logrus.Infof("Skipping secret because collection extraction failed: secretID=%s, secretResourceName=%s", secretID, secret.Name)
			continue
		}

		actualSecrets[secretID] = GCPSecret{
			Name:         secretID,
			ResourceName: secret.Name,
			Labels:       secret.Labels,
			Annotations:  secret.Annotations,
			Type:         ClassifySecret(secretID),
			Collection:   collection,
		}
	}
	return actualSecrets, nil
}

// GetSecretPayload retrieves the latest version of a secret's payload data from Google Secret Manager.
// It takes the secret resource name (e.g., "projects/my-project/secrets/my-secret") and returns the raw payload bytes.
func GetSecretPayload(ctx context.Context, client SecretManagerClient, secretResourceName string) ([]byte, error) {
	accessReq := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretResourceName + "/versions/latest",
	}
	accessResp, err := client.AccessSecretVersion(ctx, accessReq)
	if err != nil {
		return nil, fmt.Errorf("failed to access secret version: %w", err)
	}
	return accessResp.Payload.Data, nil
}
