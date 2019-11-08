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

package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	internal "github.com/banzaicloud/bank-vaults/internal/configuration"

	"github.com/banzaicloud/bank-vaults/cmd/vault-secrets-webhook/registry"
	"github.com/banzaicloud/bank-vaults/pkg/sdk/vault"
	vaultapi "github.com/hashicorp/vault/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	whhttp "github.com/slok/kubewebhook/pkg/http"
	"github.com/slok/kubewebhook/pkg/observability/metrics"
	whcontext "github.com/slok/kubewebhook/pkg/webhook/context"
	"github.com/slok/kubewebhook/pkg/webhook/mutating"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeVer "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	kubernetesConfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

var vaultAgentConfig = `
pid_file = "/tmp/pidfile"
exit_after_auth = true

auto_auth {
	method "kubernetes" {
		mount_path = "auth/%s"
		config = {
			role = "%s"
		}
	}

	sink "file" {
		config = {
			path = "/vault/.vault-token"
		}
	}
}`

type mutatingWebhook struct {
	k8sClient kubernetes.Interface
	registry  registry.ImageRegistry
}

// If the original Pod contained a Volume "vault-tls", for example Vault instances provisioned by the Operator
// we need to handle that edge case and choose another name for the vault-tls volume for accessing Vault with TLS.
func hasTLSVolume(volumes []corev1.Volume) bool {
	for _, volume := range volumes {
		if volume.Name == "vault-tls" {
			return true
		}
	}
	return false
}

func hasPodSecurityContextRunAsUser(p *corev1.PodSecurityContext) bool {
	if p.RunAsUser == nil {
		return false
	}

	return true
}

func getInitContainers(originalContainers []corev1.Container, podSecurityContext *corev1.PodSecurityContext, vaultConfig internal.VaultConfig, initContainersMutated bool, containersMutated bool, containerEnvVars []corev1.EnvVar, containerVolMounts []corev1.VolumeMount) []corev1.Container {
	var containers = []corev1.Container{}

	if vaultConfig.UseAgent || vaultConfig.CtConfigMap != "" {
		var serviceAccountMount corev1.VolumeMount

	mountSearch:
		for _, container := range originalContainers {
			for _, mount := range container.VolumeMounts {
				if mount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
					serviceAccountMount = mount
					break mountSearch
				}
			}
		}

		containerVolMounts = append(containerVolMounts, serviceAccountMount, corev1.VolumeMount{
			Name:      "vault-agent-config",
			MountPath: "/vault/agent/",
		})

		runAsUser := int64(100)
		securityContext := &corev1.SecurityContext{
			RunAsUser:                &runAsUser,
			AllowPrivilegeEscalation: &vaultConfig.PspAllowPrivilegeEscalation,
		}

		containers = append(containers, corev1.Container{
			Name:            "vault-agent",
			Image:           viper.GetString("vault_image"),
			ImagePullPolicy: corev1.PullPolicy(viper.GetString("vault_image_pull_policy")),
			SecurityContext: securityContext,
			Command:         []string{"vault", "agent", "-config=/vault/agent/config.hcl"},
			Env:             containerEnvVars,
			VolumeMounts:    containerVolMounts,
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		})
	}

	if initContainersMutated || containersMutated {
		containers = append(containers, corev1.Container{
			Name:            "copy-vault-env",
			Image:           viper.GetString("vault_env_image"),
			ImagePullPolicy: corev1.PullPolicy(viper.GetString("vault_env_image_pull_policy")),
			Command:         []string{"sh", "-c", "cp /usr/local/bin/vault-env /vault/"},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "vault-env",
					MountPath: "/vault/",
				},
			},

			SecurityContext: getSecurityContext(podSecurityContext, vaultConfig),
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			},
		})
	}

	return containers
}

func getContainers(vaultConfig internal.VaultConfig, containerEnvVars []corev1.EnvVar, containerVolMounts []corev1.VolumeMount) []corev1.Container {
	var containers = []corev1.Container{}
	securityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &vaultConfig.PspAllowPrivilegeEscalation,
	}

	if vaultConfig.CtShareProcess {
		securityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: &vaultConfig.PspAllowPrivilegeEscalation,
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"SYS_PTRACE",
				},
			},
		}
	}

	containerVolMounts = append(containerVolMounts, corev1.VolumeMount{
		Name:      "ct-secrets",
		MountPath: "/vault/secrets",
	}, corev1.VolumeMount{
		Name:      "vault-env",
		MountPath: "/home/consul-template",
	}, corev1.VolumeMount{
		Name:      "ct-configmap",
		MountPath: "/vault/ct-config/config.hcl",
		ReadOnly:  true,
		SubPath:   "config.hcl",
	},
	)

	var ctCommandString []string
	if vaultConfig.CtOnce {
		ctCommandString = []string{"-config", "/vault/ct-config/config.hcl", "-once"}
	} else {
		ctCommandString = []string{"-config", "/vault/ct-config/config.hcl"}
	}

	containers = append(containers, corev1.Container{
		Name:            "consul-template",
		Image:           vaultConfig.CtImage,
		Args:            ctCommandString,
		ImagePullPolicy: vaultConfig.CtImagePullPolicy,
		SecurityContext: securityContext,
		Env:             containerEnvVars,
		VolumeMounts:    containerVolMounts,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    vaultConfig.CtCPU,
				corev1.ResourceMemory: vaultConfig.CtMemory,
			},
		},
	})

	return containers
}

func getSecurityContext(podSecurityContext *corev1.PodSecurityContext, vaultConfig internal.VaultConfig) *corev1.SecurityContext {
	if hasPodSecurityContextRunAsUser(podSecurityContext) == true {
		return &corev1.SecurityContext{
			RunAsUser:                podSecurityContext.RunAsUser,
			AllowPrivilegeEscalation: &vaultConfig.PspAllowPrivilegeEscalation,
		}
	}

	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: &vaultConfig.PspAllowPrivilegeEscalation,
	}
}

func getVolumes(existingVolumes []corev1.Volume, agentConfigMapName string, vaultConfig internal.VaultConfig, logger *log.Logger) []corev1.Volume {
	logger.Debugf("Add generic volumes to podspec")

	volumes := []corev1.Volume{
		{
			Name: "vault-env",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
	}

	if vaultConfig.UseAgent || vaultConfig.CtConfigMap != "" {
		logger.Debugf("Add vault agent volumes to podspec")
		volumes = append(volumes, corev1.Volume{
			Name: "vault-agent-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: agentConfigMapName,
					},
				},
			},
		})
	}

	if vaultConfig.TLSSecret != "" {
		logger.Debugf("Add vault TLS volume to podspec")

		volumeName := "vault-tls"
		if hasTLSVolume(existingVolumes) {
			volumeName = "vault-env-tls"
		}

		volumes = append(volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: vaultConfig.TLSSecret,
				},
			},
		})
	}
	if vaultConfig.CtConfigMap != "" {
		logger.Debugf("Add consul template volumes to podspec")

		defaultMode := int32(420)
		volumes = append(volumes,
			corev1.Volume{
				Name: "ct-secrets",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: corev1.StorageMediumMemory,
					},
				},
			},
			corev1.Volume{
				Name: "ct-configmap",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: vaultConfig.CtConfigMap,
						},
						DefaultMode: &defaultMode,
						Items: []corev1.KeyToPath{
							{
								Key:  "config.hcl",
								Path: "config.hcl",
							},
						},
					},
				},
			})
	}

	return volumes
}

func (mw *mutatingWebhook) vaultSecretsMutator(ctx context.Context, obj metav1.Object) (bool, error) {
	switch v := obj.(type) {
	case *corev1.Pod:
		return false, mw.mutatePod(v, parseVaultConfig(obj), whcontext.GetAdmissionRequest(ctx).Namespace, whcontext.IsAdmissionRequestDryRun(ctx))
	case *corev1.Secret:
		if _, ok := obj.GetAnnotations()["vault.security.banzaicloud.io/vault-addr"]; ok {
			return false, mutateSecret(v, parseVaultConfig(obj), whcontext.GetAdmissionRequest(ctx).Namespace)
		}
		return false, nil
	case *corev1.ConfigMap:
		if _, ok := obj.GetAnnotations()["vault.security.banzaicloud.io/mutate-configmap"]; ok {
			return false, mutateConfigMap(v, parseVaultConfig(obj), whcontext.GetAdmissionRequest(ctx).Namespace)
		}
		return false, nil
	default:
		return false, nil
	}
}

func parseVaultConfig(obj metav1.Object) internal.VaultConfig {
	var vaultConfig internal.VaultConfig
	annotations := obj.GetAnnotations()

	if val, ok := annotations["vault.security.banzaicloud.io/vault-addr"]; ok {
		vaultConfig.Addr = val
	} else {
		vaultConfig.Addr = viper.GetString("vault_addr")
	}

	vaultConfig.Role = annotations["vault.security.banzaicloud.io/vault-role"]
	if vaultConfig.Role == "" {
		switch p := obj.(type) {
		case *corev1.Pod:
			vaultConfig.Role = p.Spec.ServiceAccountName
		default:
			vaultConfig.Role = "default"
		}
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-path"]; ok {
		vaultConfig.Path = val
	} else {
		vaultConfig.Path = viper.GetString("vault_path")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-skip-verify"]; ok {
		vaultConfig.SkipVerify = val
	} else {
		vaultConfig.SkipVerify = viper.GetString("vault_skip_verify")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-tls-secret"]; ok {
		vaultConfig.TLSSecret = val
	} else {
		vaultConfig.TLSSecret = viper.GetString("vault_tls_secret")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-agent"]; ok {
		vaultConfig.UseAgent, _ = strconv.ParseBool(val)
	} else {
		vaultConfig.UseAgent, _ = strconv.ParseBool(viper.GetString("vault_agent"))
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ct-configmap"]; ok {
		vaultConfig.CtConfigMap = val
	} else {
		vaultConfig.CtConfigMap = ""
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ct-image"]; ok {
		vaultConfig.CtImage = val
	} else {
		vaultConfig.CtImage = viper.GetString("vault_ct_image")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ignore-missing-secrets"]; ok {
		vaultConfig.IgnoreMissingSecrets = val
	} else {
		vaultConfig.IgnoreMissingSecrets = viper.GetString("vault_ignore_missing_secrets")
	}
	if val, ok := annotations["vault.security.banzaicloud.io/vault-env-passthrough"]; ok {
		vaultConfig.VaultEnvPassThrough = val
	} else {
		vaultConfig.VaultEnvPassThrough = viper.GetString("vault_env_passthrough")
	}
	if val, ok := annotations["vault.security.banzaicloud.io/vault-configfile-path"]; ok {
		vaultConfig.ConfigfilePath = val
	} else {
		vaultConfig.ConfigfilePath = "/vault/secrets"
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ct-pull-policy"]; ok {
		switch val {
		case "Never", "never":
			vaultConfig.CtImagePullPolicy = corev1.PullNever
		case "Always", "always":
			vaultConfig.CtImagePullPolicy = corev1.PullAlways
		case "IfNotPresent", "ifnotpresent":
			vaultConfig.CtImagePullPolicy = corev1.PullIfNotPresent
		}
	} else {
		vaultConfig.CtImagePullPolicy = corev1.PullIfNotPresent
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ct-once"]; ok {
		vaultConfig.CtOnce, _ = strconv.ParseBool(val)
	} else {
		vaultConfig.CtOnce = false
	}

	if val, err := resource.ParseQuantity(annotations["vault.security.banzaicloud.io/vault-ct-cpu"]); err != nil {
		vaultConfig.CtCPU = val
	} else {
		vaultConfig.CtCPU = resource.MustParse("100m")
	}

	if val, err := resource.ParseQuantity(annotations["vault.security.banzaicloud.io/vault-ct-memory"]); err != nil {
		vaultConfig.CtMemory = val
	} else {
		vaultConfig.CtMemory = resource.MustParse("128Mi")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/vault-ct-share-process-namespace"]; ok {
		vaultConfig.CtShareProcessDefault = "found"
		vaultConfig.CtShareProcess, _ = strconv.ParseBool(val)
	} else {
		vaultConfig.CtShareProcessDefault = "empty"
		vaultConfig.CtShareProcess = false
	}

	if val, ok := annotations["vault.security.banzaicloud.io/psp-allow-privilege-escalation"]; ok {
		vaultConfig.PspAllowPrivilegeEscalation, _ = strconv.ParseBool(val)
	} else {
		vaultConfig.PspAllowPrivilegeEscalation, _ = strconv.ParseBool(viper.GetString("psp_allow_privilege_escalation"))
	}

	if val, ok := annotations["vault.security.banzaicloud.io/mutate-configmap"]; ok {
		vaultConfig.MutateConfigMap, _ = strconv.ParseBool(val)
	} else {
		vaultConfig.MutateConfigMap, _ = strconv.ParseBool(viper.GetString("mutate_configmap"))
	}

	if val, ok := annotations["vault.security.banzaicloud.io/enable-json-log"]; ok {
		vaultConfig.EnableJSONLog = val
	} else {
		vaultConfig.EnableJSONLog = viper.GetString("enable_json_log")
	}

	if val, ok := annotations["vault.security.banzaicloud.io/transit-key-id"]; ok {
		vaultConfig.TransitKeyID = val
	}

	return vaultConfig
}

func getConfigMapForVaultAgent(obj metav1.Object, vaultConfig internal.VaultConfig) *corev1.ConfigMap {
	var ownerReferences []metav1.OwnerReference
	name := obj.GetName()
	if name == "" {
		ownerReferences = obj.GetOwnerReferences()
		if len(ownerReferences) > 0 {
			if strings.Contains(ownerReferences[0].Name, "-") {
				generateNameSlice := strings.Split(ownerReferences[0].Name, "-")
				name = strings.Join(generateNameSlice[:len(generateNameSlice)-1], "-")
			} else {
				name = ownerReferences[0].Name
			}
		}
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name + "-vault-agent-config",
			OwnerReferences: ownerReferences,
		},
		Data: map[string]string{
			"config.hcl": fmt.Sprintf(vaultAgentConfig, vaultConfig.Path, vaultConfig.Role),
		},
	}
}

func (mw *mutatingWebhook) getDataFromConfigmap(cmName string, ns string) (map[string]string, error) {
	configMap, err := mw.k8sClient.CoreV1().ConfigMaps(ns).Get(cmName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return configMap.Data, nil
}

func (mw *mutatingWebhook) getDataFromSecret(secretName string, ns string) (map[string][]byte, error) {
	secret, err := mw.k8sClient.CoreV1().Secrets(ns).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret.Data, nil
}

func (mw *mutatingWebhook) lookForEnvFrom(envFrom []corev1.EnvFromSource, ns string) ([]corev1.EnvVar, error) {
	var envVars []corev1.EnvVar

	for _, ef := range envFrom {
		if ef.ConfigMapRef != nil {
			data, err := mw.getDataFromConfigmap(ef.ConfigMapRef.Name, ns)
			if err != nil {
				if apierrors.IsNotFound(err) && ef.ConfigMapRef.Optional != nil && *ef.ConfigMapRef.Optional {
					continue
				} else {
					return envVars, err
				}
			}
			for key, value := range data {
				if hasVaultPrefix(value) {
					envFromCM := corev1.EnvVar{
						Name:  key,
						Value: value,
					}
					envVars = append(envVars, envFromCM)
				}
			}
		}
		if ef.SecretRef != nil {
			data, err := mw.getDataFromSecret(ef.SecretRef.Name, ns)
			if err != nil {
				if apierrors.IsNotFound(err) && ef.SecretRef.Optional != nil && *ef.SecretRef.Optional {
					continue
				} else {
					return envVars, err
				}
			}
			for key, value := range data {
				if hasVaultPrefix(string(value)) {
					envFromSec := corev1.EnvVar{
						Name:  key,
						Value: string(value),
					}
					envVars = append(envVars, envFromSec)
				}
			}
		}
	}
	return envVars, nil
}

func hasVaultPrefix(value string) bool {
	return strings.HasPrefix(value, "vault:") || strings.HasPrefix(value, ">>vault:")
}

func (mw *mutatingWebhook) lookForValueFrom(env corev1.EnvVar, ns string) (*corev1.EnvVar, error) {
	if env.ValueFrom.ConfigMapKeyRef != nil {
		data, err := mw.getDataFromConfigmap(env.ValueFrom.ConfigMapKeyRef.Name, ns)
		if err != nil {
			return nil, err
		}
		if hasVaultPrefix(data[env.ValueFrom.ConfigMapKeyRef.Key]) {
			fromCM := corev1.EnvVar{
				Name:  env.Name,
				Value: data[env.ValueFrom.ConfigMapKeyRef.Key],
			}
			return &fromCM, nil
		}
	}
	if env.ValueFrom.SecretKeyRef != nil {
		data, err := mw.getDataFromSecret(env.ValueFrom.SecretKeyRef.Name, ns)
		if err != nil {
			return nil, err
		}
		if hasVaultPrefix(string(data[env.ValueFrom.SecretKeyRef.Key])) {
			fromSecret := corev1.EnvVar{
				Name:  env.Name,
				Value: string(data[env.ValueFrom.SecretKeyRef.Key]),
			}
			return &fromSecret, nil
		}
	}
	return nil, nil
}

func (mw *mutatingWebhook) mutateContainers(containers []corev1.Container, podSpec *corev1.PodSpec, vaultConfig internal.VaultConfig, ns string) (bool, error) {
	mutated := false

	for i, container := range containers {
		var envVars []corev1.EnvVar
		if len(container.EnvFrom) > 0 {
			envFrom, err := mw.lookForEnvFrom(container.EnvFrom, ns)
			if err != nil {
				return false, err
			}
			envVars = append(envVars, envFrom...)
		}

		for _, env := range container.Env {
			if hasVaultPrefix(env.Value) {
				envVars = append(envVars, env)
			}
			if env.ValueFrom != nil {
				valueFrom, err := mw.lookForValueFrom(env, ns)
				if err != nil {
					return false, err
				}
				if valueFrom == nil {
					continue
				}
				envVars = append(envVars, *valueFrom)
			}
		}

		if len(envVars) == 0 {
			continue
		}

		mutated = true

		args := container.Command

		// the container has no explicitly specified command
		if len(args) == 0 {
			imageConfig, err := mw.registry.GetImageConfig(mw.k8sClient, ns, &container, podSpec)
			if err != nil {
				return false, err
			}

			args = append(args, imageConfig.Entrypoint...)

			// If no Args are defined we can use the Docker CMD from the image
			// https://kubernetes.io/docs/tasks/inject-data-application/define-command-argument-container/#notes
			if len(container.Args) == 0 {
				args = append(args, imageConfig.Cmd...)
			}
		}

		args = append(args, container.Args...)

		container.Command = []string{"/vault/vault-env"}
		container.Args = args

		container.VolumeMounts = append(container.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      "vault-env",
				MountPath: "/vault/",
			},
		}...)

		container.Env = append(container.Env, []corev1.EnvVar{
			{
				Name:  "VAULT_ADDR",
				Value: vaultConfig.Addr,
			},
			{
				Name:  "VAULT_SKIP_VERIFY",
				Value: vaultConfig.SkipVerify,
			},
			{
				Name:  "VAULT_PATH",
				Value: vaultConfig.Path,
			},
			{
				Name:  "VAULT_ROLE",
				Value: vaultConfig.Role,
			},
			{
				Name:  "VAULT_IGNORE_MISSING_SECRETS",
				Value: vaultConfig.IgnoreMissingSecrets,
			},
			{
				Name:  "VAULT_ENV_PASSTHROUGH",
				Value: vaultConfig.VaultEnvPassThrough,
			},
			{
				Name:  "VAULT_JSON_LOG",
				Value: vaultConfig.EnableJSONLog,
			},
		}...)

		if len(vaultConfig.TransitKeyID) > 0 {
			container.Env = append(container.Env, []corev1.EnvVar{
				{
					Name:  "VAULT_TRANSIT_KEY_ID",
					Value: vaultConfig.TransitKeyID,
				},
			}...)
		}

		if vaultConfig.TLSSecret != "" {
			mountPath := "/vault/tls/ca.crt"
			volumeName := "vault-tls"
			if hasTLSVolume(podSpec.Volumes) {
				mountPath = "/vault-env/tls/ca.crt"
				volumeName = "vault-env-tls"
			}

			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "VAULT_CACERT",
				Value: mountPath,
			})
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountPath,
				SubPath:   "ca.crt",
			})
		}

		if vaultConfig.UseAgent {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "VAULT_TOKEN_FILE",
				Value: "/vault/.vault-token",
			})
		}

		containers[i] = container
	}

	return mutated, nil
}

func addSecretsVolToContainers(vaultConfig internal.VaultConfig, containers []corev1.Container, logger *log.Logger) {

	for i, container := range containers {

		logger.Debugf("Add secrets VolumeMount to container %s", container.Name)

		container.VolumeMounts = append(container.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      "ct-secrets",
				MountPath: vaultConfig.ConfigfilePath,
			},
		}...)

		containers[i] = container
	}
}

func newVaultClient(vaultConfig internal.VaultConfig) (*vault.Client, error) {
	clientConfig := vaultapi.DefaultConfig()
	clientConfig.Address = vaultConfig.Addr

	vaultInsecure, err := strconv.ParseBool(vaultConfig.SkipVerify)
	if err != nil {
		return nil, fmt.Errorf("could not parse VAULT_SKIP_VERIFY")
	}

	tlsConfig := vaultapi.TLSConfig{Insecure: vaultInsecure}

	clientConfig.ConfigureTLS(&tlsConfig)

	return vault.NewClientFromConfig(
		clientConfig,
		vault.ClientRole(vaultConfig.Role),
		vault.ClientAuthPath(vaultConfig.Path),
	)
}

func newK8SClient() (kubernetes.Interface, error) {
	kubeConfig, err := kubernetesConfig.GetConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(kubeConfig)
}

func (mw *mutatingWebhook) mutatePod(pod *corev1.Pod, vaultConfig internal.VaultConfig, ns string, dryRun bool) error {

	logger.Debugf("Successfully connected to the API")

	initContainersMutated, err := mw.mutateContainers(pod.Spec.InitContainers, &pod.Spec, vaultConfig, ns)
	if err != nil {
		return err
	}

	if initContainersMutated {
		logger.Debugf("Successfully mutated pod init containers")
	} else {
		logger.Debugf("No pod init containers were mutated")
	}

	containersMutated, err := mw.mutateContainers(pod.Spec.Containers, &pod.Spec, vaultConfig, ns)
	if err != nil {
		return err
	}

	if containersMutated {
		logger.Debugf("Successfully mutated pod containers")
	} else {
		logger.Debugf("No pod containers were mutated")
	}

	containerEnvVars := []corev1.EnvVar{
		{
			Name:  "VAULT_ADDR",
			Value: vaultConfig.Addr,
		},
		{
			Name:  "VAULT_SKIP_VERIFY",
			Value: vaultConfig.SkipVerify,
		},
	}
	containerVolMounts := []corev1.VolumeMount{
		{
			Name:      "vault-env",
			MountPath: "/vault/",
		},
	}
	if vaultConfig.TLSSecret != "" {

		mountPath := "/vault/tls/ca.crt"
		volumeName := "vault-tls"
		if hasTLSVolume(pod.Spec.Volumes) {
			mountPath = "/vault-env/tls/ca.crt"
			volumeName = "vault-env-tls"
		}

		containerEnvVars = append(containerEnvVars, corev1.EnvVar{
			Name:  "VAULT_CACERT",
			Value: mountPath,
		})
		containerVolMounts = append(containerVolMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
			SubPath:   "ca.crt",
		})
	}

	if initContainersMutated || containersMutated || vaultConfig.CtConfigMap != "" {
		var agentConfigMapName string

		if vaultConfig.UseAgent || vaultConfig.CtConfigMap != "" {
			configMap := getConfigMapForVaultAgent(pod, vaultConfig)
			agentConfigMapName = configMap.Name

			if !dryRun {
				_, err := mw.k8sClient.CoreV1().ConfigMaps(ns).Create(configMap)
				if err != nil {
					if errors.IsAlreadyExists(err) {
						_, err = mw.k8sClient.CoreV1().ConfigMaps(ns).Update(configMap)
						if err != nil {
							return err
						}
					} else {
						return err
					}
				}
			}

		}

		pod.Spec.InitContainers = append(getInitContainers(pod.Spec.Containers, pod.Spec.SecurityContext, vaultConfig, initContainersMutated, containersMutated, containerEnvVars, containerVolMounts), pod.Spec.InitContainers...)
		logger.Debugf("Successfully appended pod init containers to spec")

		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolumes(pod.Spec.Volumes, agentConfigMapName, vaultConfig, logger)...)
		logger.Debugf("Successfully appended pod spec volumes")
	}

	if vaultConfig.CtConfigMap != "" {
		logger.Debugf("Consul Template config found")

		addSecretsVolToContainers(vaultConfig, pod.Spec.Containers, logger)

		if vaultConfig.CtShareProcessDefault == "empty" {
			logger.Debugf("Test our Kubernetes API Version and make the final decision on enabling ShareProcessNamespace")
			apiVersion, _ := mw.k8sClient.Discovery().ServerVersion()
			versionCompared := kubeVer.CompareKubeAwareVersionStrings("v1.12.0", apiVersion.String())
			logger.Debugf("Kubernetes API version detected: %s", apiVersion.String())

			if versionCompared >= 0 {
				vaultConfig.CtShareProcess = true
			} else {
				vaultConfig.CtShareProcess = false
			}
		}

		if vaultConfig.CtShareProcess {
			logger.Debugf("Detected shared process namespace")
			shareProcessNamespace := true
			pod.Spec.ShareProcessNamespace = &shareProcessNamespace
		}
		pod.Spec.Containers = append(getContainers(vaultConfig, containerEnvVars, containerVolMounts), pod.Spec.Containers...)

		logger.Debugf("Successfully appended pod containers to spec")
	}

	return nil
}

func init() {
	viper.SetDefault("vault_image", "vault:latest")
	viper.SetDefault("vault_image_pull_policy", string(corev1.PullIfNotPresent))
	viper.SetDefault("vault_env_image", "banzaicloud/vault-env:latest")
	viper.SetDefault("vault_env_image_pull_policy", string(corev1.PullIfNotPresent))
	viper.SetDefault("vault_ct_image", "hashicorp/consul-template:0.19.6-dev-alpine")
	viper.SetDefault("vault_addr", "https://vault:8200")
	viper.SetDefault("vault_skip_verify", "false")
	viper.SetDefault("vault_path", "kubernetes")
	viper.SetDefault("vault_tls_secret", "")
	viper.SetDefault("vault_agent", "false")
	viper.SetDefault("vault_ct_share_process_namespace", "")
	viper.SetDefault("psp_allow_privilege_escalation", "false")
	viper.SetDefault("vault_ignore_missing_secrets", "false")
	viper.SetDefault("vault_env_passthrough", "")
	viper.SetDefault("mutate_configmap", "false")
	viper.SetDefault("tls_cert_file", "")
	viper.SetDefault("tls_private_key_file", "")
	viper.SetDefault("listen_address", ":8443")
	viper.SetDefault("telemetry_listen_address", "")
	viper.SetDefault("default_image_pull_secret", "")
	viper.SetDefault("default_image_pull_secret_namespace", "")
	viper.SetDefault("default_image_pull_docker_config_json_key", corev1.DockerConfigJsonKey)
	viper.SetDefault("registry_skip_verify", "false")
	viper.SetDefault("debug", "false")
	viper.SetDefault("enable_json_log", "false")
	viper.SetDefault("registry_cache_storage", "inmemory")
	viper.SetDefault("registry_cache_digest_only", "false")
	viper.AutomaticEnv()

	logger = log.New()
	// Add additonal fields to all log messages
	logger.AddHook(&GlobalHook{})

	if viper.GetBool("enable_json_log") {
		logger.SetFormatter(&log.JSONFormatter{})
	}

	if viper.GetBool("debug") {
		logger.SetLevel(log.DebugLevel)
		logger.Debug("Debug mode enabled")
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
}

func handlerFor(config mutating.WebhookConfig, mutator mutating.MutatorFunc, recorder metrics.Recorder, logger *log.Logger) http.Handler {
	webhook, err := mutating.NewWebhook(config, mutator, nil, recorder, logger)
	if err != nil {
		logger.Fatalf("error creating webhook: %s", err)
	}

	handler, err := whhttp.HandlerFor(webhook)
	if err != nil {
		logger.Fatalf("error creating webhook: %s", err)
	}

	return handler
}

var logger *log.Logger

// GlobalHook struct used for adding additional fields to the log
type GlobalHook struct {
}

// Levels returning all log levels
func (h *GlobalHook) Levels() []log.Level {
	return log.AllLevels
}

// Fire adding the additional fields to all log entries
func (h *GlobalHook) Fire(e *log.Entry) error {
	e.Data["app"] = "vault-secrets-webhook"
	return nil
}

func serveMetrics(addr string) {
	logger.Infof("Telemetry on http://%s", addr)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	err := http.ListenAndServe(addr, mux)
	if err != nil {
		logger.Fatalf("error serving telemetry: %s", err)
	}
}

func main() {
	k8sClient, err := newK8SClient()
	if err != nil {
		logger.Fatalf("error creating k8s client: %s", err)
	}

	mutatingWebhook := mutatingWebhook{
		k8sClient: k8sClient,
		registry:  registry.NewRegistry(),
	}

	mutator := mutating.MutatorFunc(mutatingWebhook.vaultSecretsMutator)

	metricsRecorder := metrics.NewPrometheus(prometheus.DefaultRegisterer)

	podHandler := handlerFor(mutating.WebhookConfig{Name: "vault-secrets-pods", Obj: &corev1.Pod{}}, mutator, metricsRecorder, logger)
	secretHandler := handlerFor(mutating.WebhookConfig{Name: "vault-secrets-secret", Obj: &corev1.Secret{}}, mutator, metricsRecorder, logger)
	configMapHandler := handlerFor(mutating.WebhookConfig{Name: "vault-secrets-configmap", Obj: &corev1.ConfigMap{}}, mutator, metricsRecorder, logger)

	mux := http.NewServeMux()
	mux.Handle("/pods", podHandler)
	mux.Handle("/secrets", secretHandler)
	mux.Handle("/configmaps", configMapHandler)
	mux.Handle("/healthz", http.HandlerFunc(healthzHandler))

	telemetryAddress := viper.GetString("telemetry_listen_address")
	listenAddress := viper.GetString("listen_address")
	tlsCertFile := viper.GetString("tls_cert_file")
	tlsPrivateKeyFile := viper.GetString("tls_private_key_file")

	if len(telemetryAddress) > 0 {
		// Serving metrics without TLS on separated address
		go serveMetrics(telemetryAddress)
	} else {
		mux.Handle("/metrics", promhttp.Handler())
	}

	if tlsCertFile == "" && tlsPrivateKeyFile == "" {
		logger.Infof("Listening on http://%s", listenAddress)
		err = http.ListenAndServe(listenAddress, mux)
	} else {
		logger.Infof("Listening on https://%s", listenAddress)
		err = http.ListenAndServeTLS(listenAddress, tlsCertFile, tlsPrivateKeyFile, mux)
	}

	if err != nil {
		logger.Fatalf("error serving webhook: %s", err)
	}
}
