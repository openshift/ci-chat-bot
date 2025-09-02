//go:build gcs
// +build gcs

package orgdata

import (
	"context"
	"fmt"
	"log"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

// SetupGCSDataSource creates and configures a GCS data source when GCS support is enabled
func SetupGCSDataSource(ctx context.Context, gcsConfig orgdatacore.GCSConfig, orgDataService OrgDataServiceInterface) error {
	log.Printf("Loading organizational data from GCS: gs://%s/%s", gcsConfig.Bucket, gcsConfig.ObjectPath)
	gcsSource, err := orgdatacore.NewGCSDataSourceWithSDK(ctx, gcsConfig)
	if err != nil {
		return fmt.Errorf("failed to create GCS data source: %w", err)
	}

	if err := orgDataService.LoadFromDataSource(ctx, gcsSource); err != nil {
		return fmt.Errorf("failed to load organizational data from GCS: %w", err)
	}

	log.Printf("Successfully loaded organizational data from GCS")
	// Start GCS watcher for hot reload
	go func() {
		if err := orgDataService.StartDataSourceWatcher(ctx, gcsSource); err != nil {
			log.Printf("Warning: Failed to start GCS watcher: %v", err)
		}
	}()

	return nil
}
