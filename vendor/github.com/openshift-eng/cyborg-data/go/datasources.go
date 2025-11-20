package orgdatacore

import (
	"context"
	"fmt"
	"io"
)

// GCSDataSource is a stub - build with -tags gcs for actual GCS support.
type GCSDataSource struct {
	Config GCSConfig
}

func NewGCSDataSource(config GCSConfig) *GCSDataSource {
	return &GCSDataSource{Config: config}
}

func (g *GCSDataSource) Load(ctx context.Context) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%w: build with '-tags gcs' and use NewGCSDataSourceWithSDK()", ErrGCSNotEnabled)
}

func (g *GCSDataSource) Watch(ctx context.Context, callback func() error) error {
	return fmt.Errorf("%w: build with '-tags gcs' and use NewGCSDataSourceWithSDK()", ErrGCSNotEnabled)
}

func (g *GCSDataSource) String() string {
	return fmt.Sprintf("gs://%s/%s (stub)", g.Config.Bucket, g.Config.ObjectPath)
}

func (g *GCSDataSource) Close() error { return nil }
