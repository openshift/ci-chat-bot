//go:build !gcs
// +build !gcs

package orgdata

import (
	"context"
	"fmt"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

// LoadFromGCS returns an error when GCS support is not enabled
func (s *slackOrgDataService) LoadFromGCS(ctx context.Context, config orgdatacore.GCSConfig) error {
	return fmt.Errorf("GCS support not enabled. Build with '-tags gcs' to enable GCS functionality")
}

// StartGCSWatcher returns an error when GCS support is not enabled
func (s *slackOrgDataService) StartGCSWatcher(ctx context.Context, config orgdatacore.GCSConfig) error {
	return fmt.Errorf("GCS support not enabled. Build with '-tags gcs' to enable GCS functionality")
}
