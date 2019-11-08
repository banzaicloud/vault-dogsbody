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
	"strings"

	imagev1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImageCache interface
type ImageCache interface {
	Get(image string) *imagev1.ImageConfig
	Put(image string, imageConfig *imagev1.ImageConfig)
}

// ImageCacheOptions additional caching options
type ImageCacheOptions struct {
	// DigestOnly used for more sensitive caching, when you are using same
	// tags for Docker-images that can change as can change the entrypoint.
	//
	// Setting as `true` may increase network traffic on Docker Registry.
	DigestOnly bool
}

// NewImageCache returns cache by storage name
func NewImageCache(storage string, opts *ImageCacheOptions) ImageCache {
	if opts == nil {
		opts = &ImageCacheOptions{}
	}
	switch strings.ToLower(storage) {
	case "none":
		return NewNoneImageCache()
	case "inmemory":
		return NewInMemoryImageCache(opts)
	}
	return NewInMemoryImageCache(opts)
}
