package api

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/secretutil"
)

// prowArtifactsEnv is the directory Prow wants us to put artifacts into for upload
const prowArtifactsEnv string = "ARTIFACTS"

func Artifacts() (string, bool) {
	return os.LookupEnv(prowArtifactsEnv)
}

// SaveArtifact saves the data under the path relative to the artifact directory.
// If no artifact directory is set, we no-op.
// A note on censoring: SaveArtifact will ensure that the raw data being written
// to an artifact file is censored, but care must be taken by the callers of this
// utility to pre-censor fields that get materially changed or reformatted during
// encoding. For example, if a secret value contains newlines or quotes, then is
// used as a field in a JSON or XML representation, when the value is encoded into
// the `data []byte` that's passed here, censoring the raw bytes will not be sufficient
// as they will be materially different from the actual secret value. (A literal
// newline in the raw secret will be an escaped `\n` in the encoded bytes.)
func SaveArtifact(censor secretutil.Censorer, relPath string, data []byte) error {
	artifactDir, set := os.LookupEnv(prowArtifactsEnv)
	if !set {
		return nil
	}
	censor.Censor(&data)
	path := filepath.Join(artifactDir, relPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0777); err != nil {
		logrus.WithError(err).Warn("Unable to create artifact directory.")
		return err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		logrus.WithError(err).Errorf("Failed to write %s", relPath)
		return err
	}
	return nil
}
