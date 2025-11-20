//go:build gcs

package orgdatacore

import (
	"log/slog"
	"time"
)

// GCSOption configures a GCS data source.
type GCSOption func(*gcsConfig)

type gcsConfig struct {
	bucket          string
	objectPath      string
	projectID       string
	checkInterval   time.Duration
	credentialsJSON string
	logger          *slog.Logger
}

func defaultGCSConfig() *gcsConfig {
	return &gcsConfig{
		checkInterval: 5 * time.Minute,
		logger:        slog.Default(),
	}
}

// WithCheckInterval sets how often the GCS source checks for updates.
func WithCheckInterval(d time.Duration) GCSOption {
	return func(c *gcsConfig) {
		if d > 0 {
			c.checkInterval = d
		}
	}
}

// WithCredentialsJSON sets the service account credentials JSON.
func WithCredentialsJSON(creds string) GCSOption {
	return func(c *gcsConfig) {
		c.credentialsJSON = creds
	}
}

// WithProjectID sets the GCP project ID.
func WithProjectID(projectID string) GCSOption {
	return func(c *gcsConfig) {
		c.projectID = projectID
	}
}

// WithGCSLogger sets a custom logger for the GCS data source.
func WithGCSLogger(logger *slog.Logger) GCSOption {
	return func(c *gcsConfig) {
		if logger != nil {
			c.logger = logger
		}
	}
}

