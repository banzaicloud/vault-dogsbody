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

	imagev1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNoneImageCache_PutGet(t *testing.T) {
	cache := NewNoneImageCache()
	images := []string{
		"foobar:latest",
		"foobar@sha256:ABCD",
	}
	for _, image := range images {
		cache.Put(image, &imagev1.ImageConfig{
			Cmd: []string{"/bin/bash"},
		})
		if ic := cache.Get(image); ic != nil {
			t.Error("NoneImageCache.Get() != nil")
		}
	}
}
