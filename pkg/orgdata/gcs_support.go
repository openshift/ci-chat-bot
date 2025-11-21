//go:build gcs
// +build gcs

package orgdata

import (
	"context"
	"fmt"

	orgdatacore "github.com/openshift-eng/cyborg-data"
	"k8s.io/klog/v2"
)

// SetupGCSDataSource creates and configures a GCS data source when GCS support is enabled
func SetupGCSDataSource(ctx context.Context, gcsConfig orgdatacore.GCSConfig, orgDataService orgdatacore.ServiceInterface) error {
	klog.Infof("Loading organizational data from GCS: gs://%s/%s", gcsConfig.Bucket, gcsConfig.ObjectPath)

	// Create GCS data source with SDK
	gcsSource, err := orgdatacore.NewGCSDataSourceWithSDK(ctx, gcsConfig)
	if err != nil {
		return fmt.Errorf("failed to create GCS data source: %w", err)
	}

	// Load initial data
	if err := orgDataService.LoadFromDataSource(ctx, gcsSource); err != nil {
		// Don't forget to close the client on error
		if closeErr := gcsSource.Close(); closeErr != nil {
			klog.Warningf("Failed to close GCS client after load error: %v", closeErr)
		}
		return fmt.Errorf("failed to load organizational data from GCS: %w", err)
	}

	klog.Info("Successfully loaded organizational data from GCS")

	// Start GCS watcher for hot reload
	go func() {
		if err := orgDataService.StartDataSourceWatcher(ctx, gcsSource); err != nil {
			klog.Warningf("Failed to start GCS watcher: %v", err)
		}
	}()

	return nil
}
