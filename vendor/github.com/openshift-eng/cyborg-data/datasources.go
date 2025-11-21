package orgdatacore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileDataSource loads organizational data from local files
type FileDataSource struct {
	FilePaths []string
	// PollInterval controls how frequently files are checked for changes.
	// If zero, a default of 60s is used.
	PollInterval time.Duration
}

// NewFileDataSource creates a new file-based data source
// If multiple paths provided, the last one is used (allows for fallback logic)
func NewFileDataSource(filePaths ...string) *FileDataSource {
	return &FileDataSource{
		FilePaths: filePaths,
	}
}

// Load returns a reader for the organizational data file
func (f *FileDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	if len(f.FilePaths) == 0 {
		return nil, fmt.Errorf("no file paths provided")
	}

	// Load the primary data file (use the last path if multiple provided)
	filePath := f.FilePaths[len(f.FilePaths)-1]
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	return file, nil
}

// Watch monitors for file changes (basic implementation using polling)
func (f *FileDataSource) Watch(ctx context.Context, callback func() error) error {
	if len(f.FilePaths) == 0 {
		return fmt.Errorf("no file paths to watch")
	}

	// Get initial modification times
	modTimes := make(map[string]time.Time)
	for _, path := range f.FilePaths {
		if stat, err := os.Stat(path); err == nil {
			modTimes[path] = stat.ModTime()
		}
	}

	// Poll for changes based on configured interval (default 60s)
	interval := f.PollInterval
	if interval == 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Check if any files have changed
				changed := false
				for _, path := range f.FilePaths {
					if stat, err := os.Stat(path); err == nil {
						if lastMod, exists := modTimes[path]; !exists || stat.ModTime().After(lastMod) {
							modTimes[path] = stat.ModTime()
							changed = true
						}
					}
				}

				if changed {
					if err := callback(); err != nil {
						fmt.Printf("File watch callback failed: %v\n", err)
					}
				}
			}
		}
	}()

	return nil
}

// String returns a description of this data source
func (f *FileDataSource) String() string {
	if len(f.FilePaths) == 1 {
		return fmt.Sprintf("file:%s", f.FilePaths[0])
	}
	return fmt.Sprintf("files:%s", filepath.Join(f.FilePaths...))
}

// GCSDataSource is a placeholder for GCS support
// For actual GCS functionality, build with the "gcs" build tag and use NewGCSDataSourceWithSDK()
//
// To enable GCS support:
//  1. Add GCS SDK dependency: go get cloud.google.com/go/storage
//  2. Build with GCS support: go build -tags gcs
//  3. Use NewGCSDataSourceWithSDK() instead of NewGCSDataSource()
type GCSDataSource struct {
	Config GCSConfig
}

// NewGCSDataSource creates a stub GCS data source (requires build tag "gcs" for actual functionality)
func NewGCSDataSource(config GCSConfig) *GCSDataSource {
	return &GCSDataSource{Config: config}
}

// Load always returns an error - use build tag "gcs" for actual GCS support
func (g *GCSDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	return nil, fmt.Errorf("GCS support not enabled. Build with '-tags gcs' and use NewGCSDataSourceWithSDK()")
}

// Watch always returns an error - use build tag "gcs" for actual GCS support
func (g *GCSDataSource) Watch(ctx context.Context, callback func() error) error {
	return fmt.Errorf("GCS support not enabled. Build with '-tags gcs' and use NewGCSDataSourceWithSDK()")
}

// String returns a description of this data source
func (g *GCSDataSource) String() string {
	return fmt.Sprintf("gs://%s/%s (stub - build with -tags gcs for actual support)", g.Config.Bucket, g.Config.ObjectPath)
}
