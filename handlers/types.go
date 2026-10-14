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

package handlers

import (
	"sync"

	"chisel/contents"
	"chisel/stats"
	"chisel/storage"
	"chisel/types"
)

// GitClientCreator creates a GitClient at provided path.
type GitClientCreator func(path string, maxWorkers int) GitClient

// StatsDifferCreator creates a StatsDiffer at provided path.
type StatsDifferCreator func(opts *stats.DifferOpts) (StatsDiffer, error)

// ContentsDifferCreator creates a ContentsDiffer at provided path.
type ContentsDifferCreator func(opts *contents.DifferOpts) (ContentsDiffer, error)

// Project describes a project's name and path.
type Project struct {
	Name string
	Path string
}

// InitializeOptions are options to configure Initialize.
type InitializeOptions struct {
	ApplicationConfig *types.ApplicationConfig
	GitClientCreator  GitClientCreator
}

// StatsOptions are options for configuring Stats behavior.
type StatsOptions struct {
	ApplicationConfig  *types.ApplicationConfig
	Cache              Cache
	WriteAheadCache    Cache
	Storage            storage.Storage
	EmailMapper        func(email string) (string, error)
	GitClientCreator   GitClientCreator
	StatsDifferCreator StatsDifferCreator
}

// ContentsOptions are options for configuring Contents behavior.
type ContentsOptions struct {
	ApplicationConfig     *types.ApplicationConfig
	Cache                 Cache
	WriteAheadCache       Cache
	Storage               storage.Storage
	GitClientCreator      GitClientCreator
	ContentsDifferCreator ContentsDifferCreator
	// Only analyze diff contents for these files.
	// If empty, all files are analyzed.
	Files []string
}

// AllHashes is a container for present hashes found in storage as well as
// hashes encountered in concurrent project runs.
type AllHashes struct {
	mutex sync.Mutex
	h     map[int64]bool
}
