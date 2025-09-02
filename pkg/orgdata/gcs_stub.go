//go:build !gcs
// +build !gcs

package orgdata

import (
	"context"
	"log"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

// SetupGCSDataSource logs a warning when GCS support is not enabled
func SetupGCSDataSource(ctx context.Context, gcsConfig orgdatacore.GCSConfig, orgDataService OrgDataServiceInterface) {
	log.Printf("GCS support not enabled. Build with '-tags gcs' to enable GCS functionality")
	log.Printf("Skipping GCS setup for gs://%s/%s", gcsConfig.Bucket, gcsConfig.ObjectPath)
}
