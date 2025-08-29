//go:build gcs
// +build gcs

package orgdata

import (
	"context"
	"fmt"

	orgdatacore "github.com/openshift/ci-chat-bot/pkg/orgdata-core"
)

// LoadFromGCS loads organizational data from Google Cloud Storage
func (s *slackOrgDataService) LoadFromGCS(ctx context.Context, config orgdatacore.GCSConfig) error {
	// Create GCS data source and store it for later use in watcher
	gcsSource, err := orgdatacore.NewGCSDataSourceWithSDK(ctx, config)
	if err != nil {
		return err
	}

	// Store the GCS source for watching
	s.dataSource = gcsSource

	// Load data from GCS
	return s.core.LoadFromDataSource(ctx, gcsSource)
}

// StartGCSWatcher starts watching GCS for data changes
func (s *slackOrgDataService) StartGCSWatcher(ctx context.Context, config orgdatacore.GCSConfig) error {
	// Use the same GCS source that was created during LoadFromGCS
	if s.dataSource == nil {
		return fmt.Errorf("GCS source not initialized. Call LoadFromGCS first")
	}

	// Start watching for changes using the same data source instance
	return s.core.StartDataSourceWatcher(ctx, s.dataSource)
}
