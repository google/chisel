// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package cache provides types to put and get key-value pairs.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// FileCache is a simple serializer of bytes
// to and from disk.
type FileCache struct {
	Path string
}

// New creates a new FileCache at `path`.
func New(path string) (*FileCache, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	return &FileCache{path}, nil
}

// Put adds the {key,value} to disk.
func (f *FileCache) Put(key string, value any) error {
	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(f.Path, key), bytes, 0644)
}

// Delete removes the key from the cache.
func (f *FileCache) Delete(key string) error {
	return os.Remove(filepath.Join(f.Path, key))
}

// Get retrieves the key from disk.  The value is returned at the address
// of the `out` parameter.
func (f *FileCache) Get(key string, out any) error {
	data, err := os.ReadFile(filepath.Join(f.Path, key))
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &out)
}
