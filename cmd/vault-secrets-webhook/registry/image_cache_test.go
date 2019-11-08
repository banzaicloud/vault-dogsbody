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

import "testing"

func TestNewImageCache(t *testing.T) {
	tests := []string{
		"none",
		"inmemory",
		"redis",
		"memcache",
	}
	for _, test := range tests {
		cache := NewImageCache(test, nil)
		if cache == nil {
			t.Errorf("NewImageCache(%s, nil) == nil", test)
		}
		opts := &ImageCacheOptions{}
		cache = NewImageCache(test, opts)
		if cache == nil {
			t.Errorf("NewImageCache(%s, %v) == nil", test, opts)
		}
	}
}
