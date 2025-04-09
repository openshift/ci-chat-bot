package utils

import (
	"context"
	"fmt"
	"strings"
	"time"

	buildconfigclientset "github.com/openshift/client-go/build/clientset/versioned"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var LaunchLabel = "ci-chat-bot.openshift.io/launch"

const BaseDomain = "ci-chat-bot/base-domain"
const CoreOSURL = "https://coreos.slack.com"
const UserTag = "ci-chat-bot/user"
const ChannelTag = "ci-chat-bot/channel"
const ExpiryTimeTag = "ci-chat-bot/expiry-time"
const RequestTimeTag = "ci-chat-bot/request-time"
const CustomImageTag = "ci-chat-bot/custom-image"
const UserNotifiedTag = "ci-chat-bot/user-notified"

type BuildClusterClientConfig struct {
	CoreConfig        *rest.Config
	CoreClient        *clientset.Clientset
	ProjectClient     *projectclientset.Clientset
	TargetImageClient *imageclientset.Clientset
	BuildConfigClient *buildconfigclientset.Clientset
}

type BuildClusterClientConfigMap map[string]*BuildClusterClientConfig

func StripLinks(input string) string {
	var b strings.Builder
	for {
		open := strings.Index(input, "<")
		if open == -1 {
			b.WriteString(input)
			break
		}
		close := strings.Index(input[open:], ">")
		if close == -1 {
			b.WriteString(input)
			break
		}
		pipe := strings.Index(input[open:], "|")
		if pipe == -1 || pipe > close {
			b.WriteString(input[0:open])
			b.WriteString(input[open+1 : open+close])
			input = input[open+close+1:]
			continue
		}
		b.WriteString(input[0:open])
		b.WriteString(input[open+pipe+1 : open+close])
		input = input[open+close+1:]
	}
	return b.String()
}

func ParamsFromAnnotation(value string) (map[string]string, error) {
	values := make(map[string]string)
	if len(value) == 0 {
		return values, nil
	}
	for _, part := range strings.Split(value, ",") {
		if len(part) == 0 {
			return nil, fmt.Errorf("parameter may not be empty")
		}
		parts := strings.SplitN(part, "=", 2)
		key := strings.TrimSpace(parts[0])
		if len(key) == 0 {
			return nil, fmt.Errorf("parameter name may not be empty")
		}
		if len(parts) == 1 {
			values[key] = ""
			continue
		}
		values[key] = parts[1]
	}
	return values, nil
}

func Contains(arr []string, s string) bool {
	for _, item := range arr {
		if s == item {
			return true
		}
	}
	return false
}

// LoadKubeconfig loads connection configuration
// for the cluster we're deploying to. We prefer to
// use in-cluster configuration if possible, but will
// fall back to using default rules otherwise.
func LoadKubeconfig() (*rest.Config, string, bool, error) {
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{})
	clusterConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, "", false, fmt.Errorf("could not load client configuration: %w", err)
	}
	ns, isSet, err := cfg.Namespace()
	if err != nil {
		return nil, "", false, fmt.Errorf("could not load client namespace: %w", err)
	}
	return clusterConfig, ns, isSet, nil
}

func LoadKubeconfigFromFlagOrDefault(path string, def *rest.Config) (*rest.Config, error) {
	if path == "" {
		return def, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path}, &clientcmd.ConfigOverrides{},
	).ClientConfig()
}

func UpdateSecret(name string, client v1.SecretInterface, fn func(*corev1.Secret)) error {
	var updateSuccess bool
	for i := 0; i < 10; i++ {
		currentMap, err := client.Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				newMap := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: name,
					},
					Data: map[string][]byte{},
				}
				fn(newMap)
				_, err := client.Create(context.TODO(), newMap, metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("failed to create `%s` configmap: %w", name, err)
				}
				return nil
			}
			return fmt.Errorf("failed to update `%s` configmap: %w", name, err)
		}
		if currentMap.Data == nil {
			currentMap.Data = map[string][]byte{}
		}
		fn(currentMap)
		if _, err := client.Update(context.TODO(), currentMap, metav1.UpdateOptions{}); err != nil {
			// just retry after 1 second wait on conflict
			if errors.IsConflict(err) {
				time.Sleep(time.Second)
				continue
			} else {
				return fmt.Errorf("failed to update `%s` configmap: %w", name, err)
			}
		}
		updateSuccess = true
		break
	}
	if !updateSuccess {
		return fmt.Errorf("failed to update `%s` configmap after 10 retries", name)
	}
	return nil
}
