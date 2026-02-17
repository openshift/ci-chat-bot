package common

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/klog"
)

// FetchReleases retrieves the accepted release streams from the OpenShift release controller
func FetchReleases(client *http.Client, architecture string) (map[string][]string, error) {
	url := fmt.Sprintf("https://%s.ocp.releases.ci.openshift.org/api/v1/releasestreams/accepted", architecture)
	acceptedReleases := make(map[string][]string, 0)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			klog.Errorf("Failed to close response for FetchReleases: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FetchReleases: unexpected status %d from %s", resp.StatusCode, url)
	}
	if err := json.NewDecoder(resp.Body).Decode(&acceptedReleases); err != nil {
		return nil, err
	}
	return acceptedReleases, nil
}
