package orgdatacore

import (
	"context"
	"fmt"
	"io"
)

// GCSDataSource is a stub for GCS support. This is the ONLY supported production data source.
//
// IMPORTANT: File-based data sources have been removed from the public API for security reasons.
// All production deployments must use GCS with proper access controls.
//
// To enable GCS support:
//  1. Build with GCS support: go build -tags gcs
//  2. Use NewGCSDataSourceWithSDK(ctx, config) instead of NewGCSDataSource(config)
//  3. Configure GCS credentials via GOOGLE_APPLICATION_CREDENTIALS or ADC
//
// Example:
//
//	config := orgdatacore.GCSConfig{
//	    Bucket: "your-bucket",
//	    ObjectPath: "path/to/data.json",
//	    ProjectID: "your-project",
//	    CheckInterval: 5 * time.Minute,
//	}
//	source, err := orgdatacore.NewGCSDataSourceWithSDK(ctx, config)
type GCSDataSource struct {
	Config GCSConfig
}

// NewGCSDataSource creates a stub GCS data source (requires build tag "gcs" for actual functionality).
// This stub exists to allow code to compile without the GCS SDK dependency.
// For production use, build with '-tags gcs' and use NewGCSDataSourceWithSDK() instead.
func NewGCSDataSource(config GCSConfig) *GCSDataSource {
	return &GCSDataSource{Config: config}
}

// Load returns an error indicating GCS support is not enabled.
// Build with '-tags gcs' and use NewGCSDataSourceWithSDK() for actual GCS functionality.
// NOTE: File-based data sources are no longer supported for security reasons.
func (g *GCSDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	return nil, fmt.Errorf("GCS support not enabled: build with '-tags gcs' and use NewGCSDataSourceWithSDK(). " +
		"File-based data sources have been deprecated for security reasons. GCS is the only supported production data source")
}

// Watch returns an error indicating GCS support is not enabled.
// Build with '-tags gcs' and use NewGCSDataSourceWithSDK() for actual GCS functionality.
func (g *GCSDataSource) Watch(ctx context.Context, callback func() error) error {
	return fmt.Errorf("GCS support not enabled: build with '-tags gcs' and use NewGCSDataSourceWithSDK()")
}

// String returns a description of this data source
func (g *GCSDataSource) String() string {
	return fmt.Sprintf("gs://%s/%s (stub - build with -tags gcs for actual support)", g.Config.Bucket, g.Config.ObjectPath)
}
