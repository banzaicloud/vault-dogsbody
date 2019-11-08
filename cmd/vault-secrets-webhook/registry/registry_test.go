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
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseContainerImage(t *testing.T) {
	tests := []struct {
		image, name, ref string
	}{
		{
			image: "foobar",
			name:  "foobar",
			ref:   "latest",
		},
		{
			image: "foobar:latest",
			name:  "foobar",
			ref:   "latest",
		},
		{
			image: "foobar@sha256:ABCD",
			name:  "foobar",
			ref:   "sha256:ABCD",
		},
	}
	for _, test := range tests {
		name, ref := ParseContainerImage(test.image)
		if !cmp.Equal(name, test.name) {
			t.Errorf("ParseContainerImage().Name != Want \n %v", cmp.Diff(name, test.name))
		}
		if !cmp.Equal(ref, test.ref) {
			t.Errorf("ParseContainerImage().Reference != Want \n %v", cmp.Diff(ref, test.ref))
		}
	}
}

func TestContainerInfo_FixDockerHubImage(t *testing.T) {
	tests := []struct {
		image, wantImage string
		info, wantInfo   *ContainerInfo
	}{
		{
			image:     "eu.gcr.io/project/application:latest",
			wantImage: "eu.gcr.io/project/application:latest",
			info:      &ContainerInfo{},
			wantInfo:  &ContainerInfo{},
		},
		{
			image:     "redis:latest",
			wantImage: "index.docker.io/library/redis:latest",
			info:      &ContainerInfo{},
			wantInfo: &ContainerInfo{
				RegistryAddress: "https://index.docker.io",
				RegistryName:    "index.docker.io",
			},
		},
		{
			image:     "hashicorp/consul:latest",
			wantImage: "index.docker.io/hashicorp/consul:latest",
			info:      &ContainerInfo{},
			wantInfo: &ContainerInfo{
				RegistryAddress: "https://index.docker.io",
				RegistryName:    "index.docker.io",
			},
		},
	}
	for _, test := range tests {
		fixedImage := test.info.FixDockerHubImage(test.image)
		if fixedImage != test.wantImage {
			t.Errorf("FixDockerHubImage() != Want \n %v", cmp.Diff(fixedImage, test.wantImage))
		}
		if !cmp.Equal(test.info.RegistryAddress, test.wantInfo.RegistryAddress) {
			t.Errorf("FixDockerHubImage().RegistryAddress != Want \n %v", cmp.Diff(test.info.RegistryAddress, test.wantInfo.RegistryAddress))
		}
		if !cmp.Equal(test.info.RegistryName, test.wantInfo.RegistryName) {
			t.Errorf("FixDockerHubImage().RegistryName != Want \n %v", cmp.Diff(test.info.RegistryName, test.wantInfo.RegistryName))
		}
	}
}
