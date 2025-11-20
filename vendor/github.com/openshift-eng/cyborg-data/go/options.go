package orgdatacore

import "log/slog"

// ServiceOption configures a Service instance.
type ServiceOption func(*serviceConfig)

type serviceConfig struct {
	logger *slog.Logger
}

func defaultServiceConfig() *serviceConfig {
	return &serviceConfig{logger: slog.Default()}
}

// WithLogger sets a custom logger for the service.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(c *serviceConfig) {
		if logger != nil {
			c.logger = logger
		}
	}
}
