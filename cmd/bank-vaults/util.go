// Copyright © 2020 Banzai Cloud
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
	"emperror.dev/errors"
	"github.com/spf13/viper"

	internalVault "github.com/banzaicloud/bank-vaults/internal/vault"
	"github.com/banzaicloud/bank-vaults/pkg/kv"
	"github.com/banzaicloud/bank-vaults/pkg/kv/alibabakms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/alibabaoss"
	"github.com/banzaicloud/bank-vaults/pkg/kv/awskms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/azurekv"
	"github.com/banzaicloud/bank-vaults/pkg/kv/dev"
	"github.com/banzaicloud/bank-vaults/pkg/kv/file"
	"github.com/banzaicloud/bank-vaults/pkg/kv/gckms"
	"github.com/banzaicloud/bank-vaults/pkg/kv/gcs"
	"github.com/banzaicloud/bank-vaults/pkg/kv/hsm"
	"github.com/banzaicloud/bank-vaults/pkg/kv/k8s"
	"github.com/banzaicloud/bank-vaults/pkg/kv/k8srestapi"
	"github.com/banzaicloud/bank-vaults/pkg/kv/multi"
	"github.com/banzaicloud/bank-vaults/pkg/kv/s3"
	kvvault "github.com/banzaicloud/bank-vaults/pkg/kv/vault"
)

// TODO review this function's returned error
// nolint: unparam
func vaultConfigForConfig(_ *viper.Viper) (internalVault.Config, error) {
	return internalVault.Config{
		SecretShares:    appConfig.GetInt(cfgSecretShares),
		SecretThreshold: appConfig.GetInt(cfgSecretThreshold),

		InitRootToken:  appConfig.GetString(cfgInitRootToken),
		StoreRootToken: appConfig.GetBool(cfgStoreRootToken),

		PreFlightChecks: appConfig.GetBool(cfgPreFlightChecks),
	}, nil
}

// all returns true if all values of a string slice are equal to target value
func all(flags []string, target string) bool {
	for _, value := range flags {
		if value != target {
			return false
		}
	}
	return true
}

// correctValues checks whether or not all values of a slice are present in the choices slice
func correctValues(flags, choices []string) bool {
	choicesMap := make(map[string]string)
	for _, value := range choices {
		choicesMap[value] = ""
	}
	for _, value := range flags {
		if _, exists := choicesMap[value]; !exists {
			return false
		}
	}
	return true
}

func kvStoreForConfig(cfg *viper.Viper) (kv.Service, error) {
	switch mode := cfg.GetString(cfgMode); mode {
	case cfgModeValueGoogleCloudKMSGCS:
		gcs, err := gcs.New(
			cfg.GetString(cfgGoogleCloudStorageBucket),
			cfg.GetString(cfgGoogleCloudStoragePrefix),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating google cloud storage kv store")
		}

		kms, err := gckms.New(gcs,
			cfg.GetString(cfgGoogleCloudKMSProject),
			cfg.GetString(cfgGoogleCloudKMSLocation),
			cfg.GetString(cfgGoogleCloudKMSKeyRing),
			cfg.GetString(cfgGoogleCloudKMSCryptoKey),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating google cloud kms kv store")
		}

		return kms, nil

	case cfgModeValueAWSKMS3:
		var services []kv.Service

		s3Regions := cfg.GetStringSlice(cfgAWSS3Region)
		s3Buckets := cfg.GetStringSlice(cfgAWSS3Bucket)
		s3Prefix := cfg.GetString(cfgAWSS3Prefix)
		s3SSEAlgos := cfg.GetStringSlice(cfgAWS3SSEAlgo)
		kmsRegions := cfg.GetStringSlice(cfgAWSKMSRegion)
		kmsKeyIDs := cfg.GetStringSlice(cfgAWSKMSKeyID)

		if len(s3Regions) != len(s3Buckets) {
			return nil, errors.Errorf("specify the same number of regions and buckets for AWS S3 kv store [%d != %d]", len(s3Regions), len(s3Buckets))
		}

		if len(kmsRegions) != len(kmsKeyIDs) {
			return nil, errors.Errorf("specify the same number of regions and key IDs for AWS KMS kv store")
		}

		// if all the S3 buckets are using AES256 SSE then it's fine for no KMS keys to be defined
		if !all(s3SSEAlgos, awskms.SseAES256) && len(kmsRegions) != len(s3Regions) {
			return nil, errors.Errorf("specify the same number of S3 buckets and KMS keys/regions for AWS kv store."+
				"if any bucket uses AES256 SSE set its key/region to empty strings %v %v %v", kmsKeyIDs, kmsRegions, s3Buckets)
		}

		if len(s3SSEAlgos) != 0 && len(s3SSEAlgos) != len(s3Buckets) {
			return nil, errors.Errorf("specify an SSE algorithm for every S3 bucket. if a bucket has no SSE set it to an empty string")
		} else if len(s3SSEAlgos) == 0 {
			// if no SSE algorithms have been specified create an empty list. this helps ensure backwards compatibility
			s3SSEAlgos = make([]string, len(s3Buckets))
		}

		if !correctValues(s3SSEAlgos, []string{awskms.SseAES256, awskms.SseKMS, ""}) {
			return nil, errors.Errorf("you have specified one or more incorrect SSE algorithms: %v", s3SSEAlgos)
		}

		for i := 0; i < len(s3Buckets); i++ {
			var kmsKeyID string
			if s3SSEAlgos[i] == awskms.SseKMS {
				kmsKeyID = kmsKeyIDs[i]
			} else {
				kmsKeyID = ""
			}
			s3Service, err := s3.New(
				s3Regions[i],
				s3Buckets[i],
				s3Prefix,
				s3SSEAlgos[i],
				kmsKeyID,
			)
			if err != nil {
				return nil, errors.Wrap(err, "error creating AWS S3 kv store")
			}
			if s3SSEAlgos[i] == "" {
				kmsService, err := awskms.New(s3Service, kmsRegions[i], kmsKeyIDs[i])
				if err != nil {
					return nil, errors.Wrap(err, "error creating AWS KMS kv store")
				}
				services = append(services, kmsService)
			} else {
				services = append(services, s3Service)
			}
		}

		return multi.New(services), nil

	case cfgModeValueAzureKeyVault:
		akv, err := azurekv.New(cfg.GetString(cfgAzureKeyVaultName))
		if err != nil {
			return nil, errors.Wrap(err, "error creating Azure Key Vault kv store")
		}

		return akv, nil

	case cfgModeValueAlibabaKMSOSS:
		accessKeyID := cfg.GetString(cfgAlibabaAccessKeyID)
		accessKeySecret := cfg.GetString(cfgAlibabaAccessKeySecret)

		if accessKeyID == "" || accessKeySecret == "" {
			return nil, errors.Errorf("Alibaba accessKeyID or accessKeySecret can't be empty")
		}

		bucket := cfg.GetString(cfgAlibabaOSSBucket)

		if bucket == "" {
			return nil, errors.Errorf("Alibaba OSS bucket should be specified")
		}

		oss, err := alibabaoss.New(
			cfg.GetString(cfgAlibabaOSSEndpoint),
			accessKeyID,
			accessKeySecret,
			bucket,
			cfg.GetString(cfgAlibabaOSSPrefix),
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating Alibaba OSS kv store")
		}

		kms, err := alibabakms.New(
			cfg.GetString(cfgAlibabaKMSRegion),
			accessKeyID,
			accessKeySecret,
			cfg.GetString(cfgAlibabaKMSKeyID),
			oss)
		if err != nil {
			return nil, errors.Wrap(err, "error creating Alibaba KMS kv store")
		}

		return kms, nil

	case cfgModeValueVault:
		vault, err := kvvault.New(
			cfg.GetString(cfgVaultAddress),
			cfg.GetString(cfgVaultUnsealKeysPath),
			cfg.GetString(cfgVaultRole),
			cfg.GetString(cfgVaultAuthPath),
			cfg.GetString(cfgVaultTokenPath),
			cfg.GetString(cfgVaultToken))
		if err != nil {
			return nil, errors.Wrap(err, "error creating Vault kv store")
		}

		return vault, nil

	case cfgModeValueK8S:
		k8s, err := k8s.New(
			cfg.GetString(cfgK8SNamespace),
			cfg.GetString(cfgK8SSecret),
			k8sSecretLabels,
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating K8S Secret kv store")
		}

		return k8s, nil

	case cfgModeValueK8SWithRestAPI:
		k8srest, err := k8srestapi.New(
			cfg.GetString(cfgK8SWithRestAPINamespace),
			cfg.GetString(cfgK8SWithRestAPISecret),
			cfg.GetString(cfgK8SWithRestAPIKeyIdentifier),
			cfg.GetString(cfgK8SWithRestAPIEncryptionUrl),
			cfg.GetString(cfgK8SWithRestAPIDecryptionUrl),
		)

		if err != nil {
			return nil, errors.Wrap(err, "error creating K8SIBMDP Secret kv store")
		}

		return k8srest, nil

	// BANK_VAULTS_HSM_PIN=banzai bank-vaults unseal --init --mode hsm-k8s --k8s-secret-name hsm --k8s-secret-namespace default --hsm-slot-id 0
	case cfgModeValueHSMK8S:
		k8s, err := k8s.New(
			cfg.GetString(cfgK8SNamespace),
			cfg.GetString(cfgK8SSecret),
			k8sSecretLabels,
		)
		if err != nil {
			return nil, errors.Wrap(err, "error creating K8S Secret with with kv store")
		}

		config := hsm.Config{
			ModulePath: cfg.GetString(cfgHSMModulePath),
			SlotID:     cfg.GetUint(cfgHSMSlotID),
			TokenLabel: cfg.GetString(cfgHSMTokenLabel),
			Pin:        cfg.GetString(cfgHSMPin),
			KeyLabel:   cfg.GetString(cfgHSMKeyLabel),
		}

		hsm, err := hsm.New(config, k8s)
		if err != nil {
			return nil, errors.Wrap(err, "error creating HSM kv store")
		}

		return hsm, nil

	// BANK_VAULTS_HSM_PIN=banzai bank-vaults unseal --init --mode hsm --hsm-slot-id 0 --hsm-module-path /usr/local/lib/opensc-pkcs11.so
	case cfgModeValueHSM:
		config := hsm.Config{
			ModulePath: cfg.GetString(cfgHSMModulePath),
			SlotID:     cfg.GetUint(cfgHSMSlotID),
			TokenLabel: cfg.GetString(cfgHSMTokenLabel),
			Pin:        cfg.GetString(cfgHSMPin),
			KeyLabel:   cfg.GetString(cfgHSMKeyLabel),
		}

		hsm, err := hsm.New(config, nil)
		if err != nil {
			return nil, errors.Wrap(err, "error creating HSM kv store")
		}

		return hsm, nil

	case cfgModeValueDev:
		dev, err := dev.New()
		if err != nil {
			return nil, errors.Wrap(err, "error creating Dev Secret kv store")
		}

		return dev, nil

	case cfgModeValueFile:
		file, err := file.New(cfg.GetString(cfgFilePath))
		if err != nil {
			return nil, errors.Wrap(err, "error creating File kv store")
		}

		return file, nil

	default:
		return nil, errors.Errorf("unsupported backend mode: '%s'", cfg.GetString(cfgMode))
	}
}
