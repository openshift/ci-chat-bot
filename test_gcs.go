//go:build gcs
// +build gcs

package main

import (
	"context"

	orgdatacore "github.com/openshift-eng/cyborg-data"
)

func testGCS() {
	// Your IDE should now be able to find this method
	config := orgdatacore.GCSConfig{}
	_, _ = orgdatacore.NewGCSDataSourceWithSDK(context.Background(), config)
}
