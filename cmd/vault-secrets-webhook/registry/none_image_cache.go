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
	imagev1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// NoneImageCache without cache
type NoneImageCache struct{}

// NewNoneImageCache return new cache without storage
func NewNoneImageCache() ImageCache {
	return &NoneImageCache{}
}

// Get image from cache
func (c *NoneImageCache) Get(image string) *imagev1.ImageConfig {
	return nil
}

// Put image into cache
func (c *NoneImageCache) Put(image string, imageConfig *imagev1.ImageConfig) {}
