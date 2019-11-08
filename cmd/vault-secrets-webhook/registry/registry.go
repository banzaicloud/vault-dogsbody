// Copyright © 2019 Banzai Cloud
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

package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	dockerTypes "github.com/docker/docker/api/types"
	"github.com/heroku/docker-registry-client/registry"
	imagev1 "github.com/opencontainers/image-spec/specs-go/v1"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var logger *log.Logger

// ImageRegistry is a docker registry
type ImageRegistry interface {
	GetImageConfig(
		clientset kubernetes.Interface,
		namespace string,
		container *corev1.Container,
		podSpec *corev1.PodSpec) (*imagev1.ImageConfig, error)
}

// Registry impl
type Registry struct {
	imageCache ImageCache
}

// NewRegistry creates and initializes registry
func NewRegistry() ImageRegistry {
	var r *Registry = &Registry{}

	logger = log.New()
	if viper.GetBool("enable_json_log") {
		logger.SetFormatter(&log.JSONFormatter{})
	}

	imageCacheStorage := viper.GetString("registry_cache_storage")
	r.imageCache = NewImageCache(imageCacheStorage, &ImageCacheOptions{
		DigestOnly: viper.GetBool("registry_cache_digest_only"),
	})
	return r
}

type DockerCreds struct {
	Auths map[string]dockerTypes.AuthConfig `json:"auths"`
}

// GetImageConfig returns entrypoint and command of container
func (r *Registry) GetImageConfig(
	clientset kubernetes.Interface,
	namespace string,
	container *corev1.Container,
	podSpec *corev1.PodSpec) (*imagev1.ImageConfig, error) {

	if imageConfig := r.imageCache.Get(container.Image); imageConfig != nil {
		logger.Infof("found image %s in cache", container.Image)
		return imageConfig, nil
	}

	containerInfo := ContainerInfo{Namespace: namespace, clientset: clientset}

	err := containerInfo.Collect(container, podSpec)
	if err != nil {
		return nil, err
	}

	logger.Infoln("I'm using registry", containerInfo.RegistryAddress)

	imageConfig, err := getImageBlob(containerInfo)
	if imageConfig != nil {
		r.imageCache.Put(container.Image, imageConfig)
	}

	return imageConfig, err
}

// GetImageBlob download image blob from registry
func getImageBlob(container ContainerInfo) (*imagev1.ImageConfig, error) {
	imageName, reference := ParseContainerImage(container.Image)

	registrySkipVerify := viper.GetBool("registry_skip_verify")

	var hub *registry.Registry
	var err error

	if registrySkipVerify {
		hub, err = registry.NewInsecure(container.RegistryAddress, container.RegistryUsername, container.RegistryPassword)
	} else {
		hub, err = registry.New(container.RegistryAddress, container.RegistryUsername, container.RegistryPassword)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot create client for registry: %s", err.Error())
	}

	manifest, err := hub.ManifestV2(imageName, reference)
	if err != nil {
		return nil, fmt.Errorf("cannot download manifest for image: %s", err.Error())
	}

	reader, err := hub.DownloadBlob(imageName, manifest.Config.Digest)
	if reader != nil {
		defer reader.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("cannot download blob: %s", err.Error())
	}

	b, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("cannot read blob: %s", err.Error())
	}

	var imageMetadata imagev1.Image
	err = json.Unmarshal(b, &imageMetadata)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal BlobResponse JSON: %s", err.Error())
	}

	return &imageMetadata.Config, nil
}

// ParseContainerImage returns image and reference
func ParseContainerImage(image string) (string, string) {
	var split []string

	if IsImageNameWithDigest(image) {
		split = strings.SplitN(image, "@", 2)
	} else {
		split = strings.SplitN(image, ":", 2)
	}

	imageName := split[0]
	reference := "latest"

	if len(split) > 1 {
		reference = split[1]
	}

	return imageName, reference
}

// IsImageNameWithDigest check that image name contains digest as reference
func IsImageNameWithDigest(image string) bool {
	return strings.Contains(image, "@")
}

// ContainerInfo K8s structure keeps information retrieved from POD definition
type ContainerInfo struct {
	clientset        kubernetes.Interface
	Namespace        string
	ImagePullSecrets string
	RegistryAddress  string
	RegistryName     string
	RegistryUsername string
	RegistryPassword string
	Image            string
}

func (k *ContainerInfo) readDockerSecret(namespace, secretName string) (map[string][]byte, error) {
	secret, err := k.clientset.CoreV1().Secrets(namespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret.Data, nil
}

func (k *ContainerInfo) parseDockerConfig(dockerCreds DockerCreds) (bool, error) {
	for registryName, registryAuth := range dockerCreds.Auths {
		if strings.HasPrefix(registryName, "https://") {
			registryName = strings.TrimPrefix(registryName, "https://")
		}

		registryName = strings.TrimSuffix(registryName, "/")

		if strings.HasPrefix(k.Image, registryName) {
			k.RegistryName = registryName
			if registryAuth.ServerAddress != "" {
				k.RegistryAddress = registryAuth.ServerAddress
			} else {
				k.RegistryAddress = fmt.Sprintf("https://%s", registryName)
			}
			if len(registryAuth.Username) > 0 && len(registryAuth.Password) > 0 {
				// auths.<registry>.username and auths.<registry>.username are present
				// in the config.json, use them
				k.RegistryUsername = registryAuth.Username
				k.RegistryPassword = registryAuth.Password
			} else if len(registryAuth.Auth) > 0 {
				// auths.<registry>.username and auths.<registry>.username are not present
				// in the config.json, fall back to the base64 encoded auths.<registry>.auth field
				// The registry.Auth field contains a base64 encoded string of the format <username>:<password>
				decodedAuth, err := base64.StdEncoding.DecodeString(registryAuth.Auth)
				if err != nil {
					return false, fmt.Errorf("failed to decode auth field for registry %s: %s", registryName, err.Error())
				}
				auth := strings.Split(string(decodedAuth), ":")
				if len(auth) != 2 {
					return false, fmt.Errorf("unexpected number of elements in auth field for registry %s: %d (expected 2)", registryName, len(auth))
				}
				// decodedAuth is something like ":xxx"
				if len(auth[0]) <= 0 {
					return false, fmt.Errorf("username element of auth field for registry %s missing", registryName)
				}
				// decodedAuth is something like "xxx:"
				if len(auth[1]) <= 0 {
					return false, fmt.Errorf("password element of auth field for registry %s missing", registryName)
				}
				k.RegistryUsername = auth[0]
				k.RegistryPassword = auth[1]
			} else {
				// the auths section has an entry for the registry, but it neither contains
				// username/password fields nor an auth field, fail
				return false, fmt.Errorf("found %s in imagePullSecrets but it contains no usable credentials; either username/password fields or an auth field are required", registryName)
			}

			return true, nil
		}
	}

	return false, nil
}

// FixDockerHubImage to fix RegistryAddress and RegistryName for images from Docker Hub
func (k *ContainerInfo) FixDockerHubImage(image string) string {
	slash := strings.Index(image, "/")
	if slash == -1 { // Is it a DockerHub library repository?
		image = "index.docker.io/library/" + image
	} else if !strings.Contains(image[:slash], ".") { // DockerHub organization names can't contain '.'
		image = "index.docker.io/" + image
	} else {
		return image
	}

	// if in the end there is no RegistryAddress defined it should be a public DockerHub repository
	k.RegistryAddress = "https://index.docker.io"
	k.RegistryName = "index.docker.io"

	return image
}

func (k *ContainerInfo) checkImagePullSecret(namespace string, secret string) (bool, error) {
	data, err := k.readDockerSecret(namespace, secret)
	if err != nil {
		return false, fmt.Errorf("cannot read imagePullSecret '%s' in namespace '%s': %s", secret, namespace, err.Error())
	}

	var dockerCreds DockerCreds

	dockerConfigJSONKey := viper.GetString("default_image_pull_docker_config_json_key")
	err = json.Unmarshal(data[dockerConfigJSONKey], &dockerCreds)
	if err != nil {
		return false, fmt.Errorf("cannot unmarshal docker configuration from imagePullSecret: %s", err.Error())
	}

	found, err := k.parseDockerConfig(dockerCreds)
	return found, err
}

// Collect reads information from k8s and load them into the structure
func (k *ContainerInfo) Collect(container *corev1.Container, podSpec *corev1.PodSpec) error {

	k.Image = k.FixDockerHubImage(container.Image)

	var err error
	found := false
	// Check for registry credentials in imagePullSecrets attached to the pod
	// ImagePullSecrets attached to ServiceAccounts do not have to be considered
	// explicitely as ServiceAccount ImagePullSecrets are automatically attached
	// to a pod.
	for _, imagePullSecret := range podSpec.ImagePullSecrets {
		found, err = k.checkImagePullSecret(k.Namespace, imagePullSecret.Name)
		if err != nil {
			return err
		}

		if found {
			logger.Infof("found credentials for registry %s in pod imagePullSecret: %s/%s", k.RegistryName, k.Namespace, imagePullSecret.Name)
			break
		}
	}

	// The pod imagePullSecrets did not contained matching credentials.
	// Try to find matching registry credentials in the default imagePullSecret if one was provided.
	if !found {
		defaultImagePullSecret := viper.GetString("default_image_pull_secret")
		defaultImagePullSecretNamespace := viper.GetString("default_image_pull_secret_namespace")
		if len(defaultImagePullSecret) > 0 && len(defaultImagePullSecretNamespace) > 0 {
			found, err = k.checkImagePullSecret(defaultImagePullSecretNamespace, defaultImagePullSecret)
			if err != nil {
				return err
			}

			if found {
				logger.Infof("found credentials for registry %s in default imagePullSecret: %s/%s", k.RegistryName, defaultImagePullSecretNamespace, defaultImagePullSecret)
			}
		}
	}

	if !found {
		logger.Infof("found no credentials for registry %s, assuming it is public", k.RegistryName)
	}

	// In case of other public docker registry
	if k.RegistryName == "" && k.RegistryAddress == "" {
		registryName := container.Image
		if strings.HasPrefix(registryName, "https://") {
			registryName = strings.TrimPrefix(registryName, "https://")
		}

		registryName = strings.Split(registryName, "/")[0]
		k.RegistryName = registryName
		k.RegistryAddress = fmt.Sprintf("https://%s", registryName)
	}

	// Clean registry from image
	k.Image = strings.TrimPrefix(k.Image, fmt.Sprintf("%s/", k.RegistryName))

	return nil
}
