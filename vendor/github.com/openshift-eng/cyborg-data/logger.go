package orgdatacore

import (
	"log"

	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
)

// pkgLogger is a package-level logr.Logger used by this library.
var pkgLogger = stdr.New(log.Default()) //nolint:unused // Used in build tag conditional files

// SetLogger allows consumers to provide a custom logr.Logger (e.g., klogr.New()).
// It is safe to call at initialization time before concurrent use.
func SetLogger(l logr.Logger) {
	pkgLogger = l
}

func logInfo(msg string, keysAndValues ...any) { //nolint:unused // Used in build tag conditional files
	pkgLogger.Info(msg, keysAndValues...)
}

func logError(err error, msg string, keysAndValues ...any) { //nolint:unused // Used in build tag conditional files
	pkgLogger.Error(err, msg, keysAndValues...)
}
