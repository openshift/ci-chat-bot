//go:build gcs

package orgdatacore

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSDataSourceImpl is the actual GCS implementation (only available when gcs build tag is used)
type GCSDataSourceImpl struct {
	Config      GCSConfig
	client      *storage.Client
	lastModTime time.Time
}

// NewGCSDataSourceWithSDK creates a new GCS-based data source with Google Cloud Storage client support
// This function is only available when building with the "gcs" build tag
func NewGCSDataSourceWithSDK(ctx context.Context, config GCSConfig) (*GCSDataSourceImpl, error) {
	// Set default check interval if not provided
	if config.CheckInterval == 0 {
		config.CheckInterval = 5 * time.Minute
	}

	// Create GCS client
	var client *storage.Client
	var err error

	if config.CredentialsJSON != "" {
		// Use service account credentials
		client, err = storage.NewClient(ctx, option.WithCredentialsJSON([]byte(config.CredentialsJSON)))
	} else {
		// Use default credentials (ADC - Application Default Credentials)
		client, err = storage.NewClient(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCSDataSourceImpl{
		Config: config,
		client: client,
	}, nil
}

// Load returns a reader for the GCS object
func (g *GCSDataSourceImpl) Load(ctx context.Context) (io.ReadCloser, error) {
	bucket := g.client.Bucket(g.Config.Bucket)
	object := bucket.Object(g.Config.ObjectPath)

	// Get object attributes to check last modified time
	attrs, err := object.Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GCS object attributes gs://%s/%s: %w", g.Config.Bucket, g.Config.ObjectPath, err)
	}

	// Update last modification time
	g.lastModTime = attrs.Updated

	// Create reader for the object
	reader, err := object.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS object reader gs://%s/%s: %w", g.Config.Bucket, g.Config.ObjectPath, err)
	}

	return reader, nil
}

// Watch monitors for GCS object changes using polling
func (g *GCSDataSourceImpl) Watch(ctx context.Context, callback func() error) error {
	ticker := time.NewTicker(g.Config.CheckInterval)

	go func() {
		defer ticker.Stop() // Moved inside goroutine - this was the bug fix!
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Check GCS object metadata for changes
				bucket := g.client.Bucket(g.Config.Bucket)
				object := bucket.Object(g.Config.ObjectPath)

				attrs, err := object.Attrs(ctx)
				if err != nil {
					logError(err, "GCS: Failed to check object metadata", "object", g.String())
					continue
				}

				// Check if object has been modified
				if attrs.Updated.After(g.lastModTime) {
					logInfo("GCS: Object updated, reloading organizational data", "object", g.String())
					g.lastModTime = attrs.Updated
					if err := callback(); err != nil {
						logError(err, "GCS: Reload failed", "object", g.String())
					} else {
						logInfo("GCS: Organizational data reloaded successfully", "object", g.String())
					}
				}
			}
		}
	}()

	return nil
}

// String returns a description of this data source
func (g *GCSDataSourceImpl) String() string {
	return fmt.Sprintf("gs://%s/%s", g.Config.Bucket, g.Config.ObjectPath)
}

// Close closes the GCS client
func (g *GCSDataSourceImpl) Close() error {
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}
