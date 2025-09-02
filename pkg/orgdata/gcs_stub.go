//go:build !gcs
// +build !gcs

package orgdata

import (
	"context"
	"fmt"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

// SetupGCSDataSource returns an error when GCS support is not enabled
func SetupGCSDataSource(ctx context.Context, gcsConfig orgdatacore.GCSConfig, orgDataService OrgDataServiceInterface) error {
	return fmt.Errorf("GCS support not enabled. Build with '-tags gcs' to enable GCS functionality for gs://%s/%s", gcsConfig.Bucket, gcsConfig.ObjectPath)
}
