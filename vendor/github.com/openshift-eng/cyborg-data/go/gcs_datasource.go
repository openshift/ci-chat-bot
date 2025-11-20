//go:build gcs

package orgdatacore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type GCSDataSourceImpl struct {
	bucket      string
	objectPath  string
	client      *storage.Client
	lastModTime time.Time
	interval    time.Duration
	logger      *slog.Logger
}

func NewGCSDataSourceWithSDK(ctx context.Context, bucket, objectPath string, opts ...GCSOption) (*GCSDataSourceImpl, error) {
	if bucket == "" {
		return nil, NewConfigError("bucket", "bucket name is required")
	}
	if objectPath == "" {
		return nil, NewConfigError("objectPath", "object path is required")
	}

	cfg := defaultGCSConfig()
	cfg.bucket = bucket
	cfg.objectPath = objectPath
	for _, opt := range opts {
		opt(cfg)
	}

	var clientOpts []option.ClientOption
	if cfg.credentialsJSON != "" {
		clientOpts = append(clientOpts, option.WithCredentialsJSON([]byte(cfg.credentialsJSON)))
	}

	client, err := storage.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return &GCSDataSourceImpl{
		bucket:     bucket,
		objectPath: objectPath,
		client:     client,
		interval:   cfg.checkInterval,
		logger:     cfg.logger,
	}, nil
}

func (g *GCSDataSourceImpl) Load(ctx context.Context) (io.ReadCloser, error) {
	bucket := g.client.Bucket(g.bucket)
	object := bucket.Object(g.objectPath)

	attrs, err := object.Attrs(ctx)
	if err != nil {
		return nil, NewLoadError(g.String(), fmt.Errorf("failed to get object attributes: %w", err))
	}
	g.lastModTime = attrs.Updated

	reader, err := object.NewReader(ctx)
	if err != nil {
		return nil, NewLoadError(g.String(), fmt.Errorf("failed to create reader: %w", err))
	}

	return reader, nil
}

func (g *GCSDataSourceImpl) Watch(ctx context.Context, callback func() error) error {
	ticker := time.NewTicker(g.interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				g.logger.Debug("GCS watcher stopped", "source", g.String())
				return
			case <-ticker.C:
				g.checkAndReload(ctx, callback)
			}
		}
	}()

	g.logger.Info("GCS watcher started", "source", g.String(), "interval", g.interval)
	return nil
}

func (g *GCSDataSourceImpl) checkAndReload(ctx context.Context, callback func() error) {
	attrs, err := g.client.Bucket(g.bucket).Object(g.objectPath).Attrs(ctx)
	if err != nil {
		g.logger.Error("failed to check object metadata", "source", g.String(), "error", err)
		return
	}

	if attrs.Updated.After(g.lastModTime) {
		g.logger.Info("object updated, reloading", "source", g.String())
		g.lastModTime = attrs.Updated
		if err := callback(); err != nil {
			g.logger.Error("reload failed", "source", g.String(), "error", err)
		}
	}
}

func (g *GCSDataSourceImpl) String() string {
	return fmt.Sprintf("gs://%s/%s", g.bucket, g.objectPath)
}

func (g *GCSDataSourceImpl) Close() error {
	if g.client != nil {
		return g.client.Close()
	}
	return nil
}

func (g *GCSDataSourceImpl) Bucket() string     { return g.bucket }
func (g *GCSDataSourceImpl) ObjectPath() string { return g.objectPath }
