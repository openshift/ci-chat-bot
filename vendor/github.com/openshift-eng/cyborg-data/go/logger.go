package orgdatacore

import "log/slog"

var pkgLogger = slog.Default()

// SetLogger sets the package-level logger used by internal operations.
func SetLogger(l *slog.Logger) {
	if l != nil {
		pkgLogger = l
	}
}

// GetLogger returns the current package-level logger.
func GetLogger() *slog.Logger {
	return pkgLogger
}
