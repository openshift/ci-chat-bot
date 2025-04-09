// Copyright 2020 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"

	buildv1 "github.com/openshift/api/build/v1"
	declarativeconfig "github.com/operator-framework/operator-registry/alpha/declcfg"
	registrybundle "github.com/operator-framework/operator-registry/pkg/lib/bundle"

	fbcutil "github.com/openshift/ci-chat-bot/pkg/catalog/fbcutil"
	operator "github.com/openshift/ci-chat-bot/pkg/catalog/operator"
	"github.com/openshift/ci-chat-bot/pkg/utils"
	"maps"
)

var dockerfile = `FROM registry.ci.openshift.org/origin/ubi-minimal:8 AS builder
COPY catalog-content /configs
# the opm image does not have a shell, so we must fix permissions via multistage build images
RUN chmod -R 0444 configs

# The base image is expected to contain
# /bin/opm (with a serve subcommand) and /bin/grpc_health_probe
FROM quay.io/operator-framework/opm:latest

# Configure the entrypoint and command
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]

# Copy declarative config root into image at /configs and pre-populate serve cache
COPY --from=builder /configs /configs
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

# Set DC-specific label for the location of the DC root directory
# in the image
LABEL operators.operatorframework.io.index.configs.v1=/configs
`

func CreateBuild(ctx context.Context, clients utils.BuildClusterClientConfig, bundleImage, namespace string) error {
	_, err := clients.CoreClient.CoreV1().ConfigMaps(namespace).Get(ctx, "catalog-content", metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get configmap: %w", err)
		}
		klog.Infof("Configmap for catalog build %s not found; creating", bundleImage)
		secret, err := clients.CoreClient.CoreV1().Secrets(namespace).Get(ctx, "registry-pull-credentials", metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get registry secret %s/registry-pull-credentials: %w", namespace, err)
		}
		secretAuth, ok := secret.Data[".dockerconfigjson"]
		if !ok {
			return fmt.Errorf("secret %s/reg did not contain `.dockerconfigjson` data", namespace)
		}
		content, err := CreateContent(ctx, secretAuth, bundleImage)
		if err != nil {
			return err
		}
		cm := v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "catalog-content",
			},
			Data: map[string]string{
				"catalog.json": content,
			},
		}
		if _, err := clients.CoreClient.CoreV1().ConfigMaps(namespace).Create(ctx, &cm, metav1.CreateOptions{}); err != nil {
			return err
		}
	}
	if _, err := clients.BuildConfigClient.BuildV1().Builds(namespace).Get(ctx, "catalog", metav1.GetOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get build: %w", err)
		}
		klog.Infof("Build for catalog build %s not found; creating", bundleImage)
		build := buildv1.Build{
			ObjectMeta: metav1.ObjectMeta{
				Name: "catalog",
			},
			Spec: buildv1.BuildSpec{
				CommonSpec: buildv1.CommonSpec{
					Strategy: buildv1.BuildStrategy{
						Type: buildv1.DockerBuildStrategyType,
						DockerStrategy: &buildv1.DockerBuildStrategy{
							From: &v1.ObjectReference{
								Kind: "DockerImage",
								Name: "quay.io/operator-framework/opm:latest",
							},
						},
					},
					Source: buildv1.BuildSource{
						Type:       buildv1.BuildSourceDockerfile,
						Dockerfile: &dockerfile,
						Images: []buildv1.ImageSource{{
							As: []string{"quay.io/operator-framework/opm:latest"},
							From: v1.ObjectReference{
								Kind: "DockerImage",
								Name: "quay.io/operator-framework/opm:latest",
							},
						}, {
							As: []string{"registry.ci.openshift.org/origin/ubi-minimal:8"},
							From: v1.ObjectReference{
								Kind: "DockerImage",
								Name: "registry.ci.openshift.org/origin/ubi-minimal:8",
							},
						}},
						ConfigMaps: []buildv1.ConfigMapBuildSource{{
							ConfigMap: v1.LocalObjectReference{
								Name: "catalog-content",
							},
							DestinationDir: "catalog-content",
						}},
					},
					Output: buildv1.BuildOutput{
						To: &v1.ObjectReference{
							Kind:      "ImageStreamTag",
							Namespace: namespace,
							Name:      "pipeline:catalog",
						},
					},
				},
			},
		}
		if _, err := clients.BuildConfigClient.BuildV1().Builds(namespace).Create(ctx, &build, metav1.CreateOptions{}); err != nil {
			return err
		}
		klog.Infof("Catalog build for %s created in namespace %s", bundleImage, namespace)
		if err := wait.PollUntilContextTimeout(context.TODO(), 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			build, err := clients.BuildConfigClient.BuildV1().Builds(namespace).Get(ctx, "catalog", metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			switch build.Status.Phase {
			case buildv1.BuildPhaseComplete:
				logrus.Infof("Build %s succeeded", build.Name)
				return true, nil
			case buildv1.BuildPhaseFailed, buildv1.BuildPhaseCancelled, buildv1.BuildPhaseError:
				logrus.Infof("Build %s failed, printing logs:", build.Name)
				return true, fmt.Errorf("the build %s failed with reason %s: %s\n\n%s", build.Name, build.Status.Reason, build.Status.Message, build.Status.LogSnippet)
			}
			return false, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func UpdateDockerConfigJSON(secretData []byte) error {
	configPath := filepath.Join(xdg.Home, ".docker/config.json")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			containersDir := filepath.Join(xdg.RuntimeDir, "containers")
			if _, err = os.Stat(containersDir); os.IsNotExist(err) {
				if err := os.MkdirAll(containersDir, os.ModePerm); err != nil {
					return fmt.Errorf("failed to create containers config path: %w", err)
				}
			}
			configPath = filepath.Join(containersDir, "auth.json")
			if _, err = os.Stat(configPath); err != nil {
				if os.IsNotExist(err) {
					if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
						return fmt.Errorf("failed to write container auth file: %w", err)
					}
					if _, err = os.Stat(configPath); err != nil {
						return fmt.Errorf("failed to stat newly created container auth file: %w", err)
					}
				} else {
					return fmt.Errorf("failed to stat container auth file: %w", err)
				}
			}
		} else {
			return fmt.Errorf("failed to stat docker config file: %w", err)
		}
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read container auth file: %w", err)
	}
	authMap := make(map[string]any)
	if err := json.Unmarshal(raw, &authMap); err != nil {
		return fmt.Errorf("failed to parse existing container auth config: %w", err)
	}
	newAuths := make(map[string]any)
	if err := json.Unmarshal(secretData, &newAuths); err != nil {
		return fmt.Errorf("failed to parse new container auth config: %w", err)
	}
	maps.Copy(authMap, newAuths)
	updatedRaw, err := json.Marshal(authMap)
	if err != nil {
		return fmt.Errorf("failed to marshal new auth map: %w", err)
	}

	if err := os.WriteFile(configPath, updatedRaw, 0644); err != nil {
		return fmt.Errorf("failed to write container auth file: %w", err)
	}
	return nil
}

func CreateContent(ctx context.Context, secretAuth []byte, bundleImage string) (string, error) {
	if err := UpdateDockerConfigJSON(secretAuth); err != nil {
		return "", err
	}
	// Load bundle labels and set label-dependent values.
	labels, bundle, err := operator.LoadBundle(ctx, bundleImage, false, false)
	if err != nil {
		return "", err
	}
	csv := bundle.CSV

	// FBC variables
	f := &fbcutil.FBCContext{
		Package: labels[registrybundle.PackageLabel],
		Refs:    []string{bundleImage},
		ChannelEntry: declarativeconfig.ChannelEntry{
			Name: csv.Name,
		},
	}

	// ignore channels for the bundle and instead use the default
	f.ChannelName = fbcutil.DefaultChannel

	// generate an fbc if an fbc specific label is found on the image or for a default index image.
	content, err := generateFBCContent(ctx, f, bundleImage)
	if err != nil {
		return "", fmt.Errorf("error generating File-Based Catalog content with bundle %q: %v", bundleImage, err)
	}

	return content, nil
}

// generateFBCContent creates a File-Based Catalog using the bundle image and context
func generateFBCContent(ctx context.Context, f *fbcutil.FBCContext, bundleImage string) (string, error) {
	logrus.Infof("Creating a File-Based Catalog of the bundle %q", bundleImage)
	// generate a File-Based Catalog representation of the bundle image
	bundleDeclcfg, err := f.CreateFBC(ctx)
	if err != nil {
		return "", fmt.Errorf("error creating a File-Based Catalog with image %q: %v", bundleImage, err)
	}

	declcfg := &declarativeconfig.DeclarativeConfig{
		Bundles:  []declarativeconfig.Bundle{bundleDeclcfg.Bundle},
		Packages: []declarativeconfig.Package{bundleDeclcfg.Package},
		Channels: []declarativeconfig.Channel{bundleDeclcfg.Channel},
	}

	// validate the declarative config and convert it to a string
	var content string
	if content, err = fbcutil.ValidateAndStringify(declcfg); err != nil {
		return "", fmt.Errorf("error validating and converting the declarative config object to a string format: %v", err)
	}

	logrus.Infof("Generated a valid File-Based Catalog")

	return content, nil
}
