/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// taken from test-infra/prow/config

package apiv1

import (
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
)

// Load reads the decoration_config from prowConfigPath and periodics from
// the provided job config, or returns an error.
func Load(prowConfigPath, jobConfigPath string) (*Config, error) {
	c := &Config{}
	for _, path := range []string{prowConfigPath, jobConfigPath} {
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Agent watches a path and automatically loads the config stored
// therein.
type Agent struct {
	sync.Mutex
	c *Config
}

// Start will begin polling the config file at the path. If the first load
// fails, Start with return the error and abort. Future load failures will log
// the failure message but continue attempting to load.
func (ca *Agent) Start(prowConfig, jobConfig string) error {
	c, err := Load(prowConfig, jobConfig)
	if err != nil {
		return err
	}
	ca.c = c
	go func() {
		var lastModTime time.Time
		// Rarely, if two changes happen in the same second, mtime will
		// be the same for the second change, and an mtime-based check would
		// fail. Reload periodically just in case.
		skips := 0
		for range time.Tick(1 * time.Second) {
			if skips < 600 {
				// Check if the file changed to see if it needs to be re-read.
				// os.Stat follows symbolic links, which is how ConfigMaps work.
				prowStat, err := os.Stat(jobConfig)
				if err != nil {
					glog.Errorf("Error loading prow config: %v", err)
					continue
				}

				recentModTime := prowStat.ModTime()

				jobConfigStat, err := os.Stat(jobConfig)
				if err != nil {
					glog.Errorf("Error loading job config: %v", err)
					continue
				}

				if jobConfigStat.ModTime().After(recentModTime) {
					recentModTime = jobConfigStat.ModTime()
				}

				if !recentModTime.After(lastModTime) {
					skips++
					continue // file hasn't been modified
				}
				lastModTime = recentModTime
			}
			if c, err := Load(prowConfig, jobConfig); err != nil {
				glog.Errorf("Error loading config: %v", err)
			} else {
				skips = 0
				ca.Lock()
				ca.c = c
				ca.Unlock()
			}
		}
	}()
	return nil
}

// Config returns the latest config. Do not modify the config.
func (ca *Agent) Config() *Config {
	ca.Lock()
	defer ca.Unlock()
	return ca.c
}

// Set sets the config. Useful for testing.
func (ca *Agent) Set(c *Config) {
	ca.Lock()
	defer ca.Unlock()
	ca.c = c
}
