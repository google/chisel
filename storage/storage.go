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

// Package storage defines a storage interface and a registry to enable compile
// time plugins.
package storage

import (
	"context"
	"fmt"
	"sync"

	"chisel/contents"
	"chisel/stats"
	"chisel/types"
)

// AddDiffResultsOptions are options to control behavior of storage.AddDiffResults.
type AddDiffResultsOptions struct {
	IsRetry bool
}

// AddStatsOptions are options to control behavior of storage.AddStats.
type AddStatsOptions struct {
	IsRetry bool
}

// HunksForCommitOptions are options to control behavior of storage.HunksForCommit.
type HunksForCommitOptions struct {
	Limit  int
	Offset int
}

// CommitDeltasOptions are options to control behavior of storage.CommitDeltas.
type CommitDeltasOptions struct {
	Limit  int
	Offset int
}

// FileDeltasOptions are options to control behavior of storage.MostRecentFileDeltas.
type FileDeltasOptions struct {
	Limit  int
	Offset int
}

// CommitFileDeltasOptions are options to control behavior of storage.CommitFileDeltas.
type CommitFileDeltasOptions struct {
	Limit  int
	Offset int
}

// Storage provides functions for interacting with persistent storage.
type Storage interface {
	Init(ctx context.Context) error
	MostRecentStatsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error)
	MostRecentStatsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error)
	MostRecentFiles(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]*stats.BranchFile, error)
	CurrCommits(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]bool, error)
	AddStats(ctx context.Context, projectConfig *types.ProjectConfig, statObjects *stats.StatObjects, opts *AddStatsOptions) error

	AllHashes(ctx context.Context) (map[int64]bool, error)
	MostRecentContentsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error)
	MostRecentContentsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error)
	MostRecentLiveHunks(ctx context.Context, projectConfig *types.ProjectConfig) (contents.LiveHunkSegmentsData, error)
	AddContentHashes(ctx context.Context, hashToContent map[int64][]string) error
	SplitTransactions(ctx context.Context, projectConfig *types.ProjectConfig, action string) (*types.ResultsDescriptor, error)
	AddDiffResults(ctx context.Context, projectConfig *types.ProjectConfig, diffResults *contents.DiffResults, options *AddDiffResultsOptions) error

	HunkByID(ctx context.Context, queryConfig *types.QueryConfig) (*contents.HunkListItem, error)
	SortedHunksForFile(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.HunkListItem, error)
	SortedFiles(ctx context.Context, queryConfig *types.QueryConfig) ([]string, error)
	Commit(ctx context.Context, queryConfig *types.QueryConfig) (*contents.CommitListItem, error)
	SortedCommits(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.CommitListItem, error)
	Close() error
}

// Builder is the function prototype to build a Storage implementor.
type Builder func(data ...any) (Storage, error)

var (
	mu       sync.RWMutex
	builders = make(map[string]Builder)
)

// Register registers a builder under the given name.
func Register(name string, builder Builder) {
	mu.Lock()
	defer mu.Unlock()
	if builder == nil {
		panic("builder is null")
	}
	if _, dup := builders[name]; dup {
		panic("Register called twice for builder " + name)
	}
	builders[name] = builder
}

// Lookup returns the builder matching the given name.
func Lookup(name string) (Builder, error) {
	mu.RLock()
	builder, ok := builders[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown builder %q", name)
	}

	return builder, nil
}

// ProjectDelta summarizes the deltas for a branch commit.
type ProjectDelta struct {
	Project         string
	MergeBase       string
	CommitSha1      string
	NetInsertions   int
	NetDeletions    int
	NetFilesChanged int
}

// ProjectTimeSeries is a snapshot of the deltas for a project at a given time.
type ProjectTimeSeries struct {
	CommitSha1    string
	NetInsertions int
	NetDeletions  int
	ResidentTime  int64
}

// FileDelta describes the file deltas at a commit.
type FileDelta struct {
	Path          string
	CommitSha1    string
	MergeBase     string
	NetInsertions int
	NetDeletions  int
	ResidentTime  int64
}

// CommitBugs groups a commit and related bugs.
type CommitBugs map[string][]int

// CommitDelta describes a delta for a commit.
type CommitDelta struct {
	CommitSha1       string
	ChangeID         string
	CommitType       string
	Subject          string
	Email            string
	CommitTime       int64
	MergeBase        string
	IsFirstParent    bool
	OldestDescendant string
	FilesChanged     int
	Insertions       int
	Deletions        int
	NetFilesChanged  int
	NetInsertions    int
	NetDeletions     int
	ResidentTime     int64
}

// CommitFileDelta is a denormalized structure. It groups file information with commit
// information.
type CommitFileDelta struct {
	CommitSha1            string
	CommitType            string
	Subject               string
	Email                 string
	CommitTime            int64
	CommitFilesChanged    int
	CommitInsertions      int
	CommitDeletions       int
	IsFirstParent         bool
	MergeBase             string
	OldestDescendant      string
	ResidentTime          int64
	CommitNetFilesChanged int
	CommitNetInsertions   int
	CommitNetDeletions    int
	Path                  string
	FileNetInsertions     int
	FileNetDeletions      int
}

// DownstreamUpstreamPairing represents a downstream/upstream pairing.
type DownstreamUpstreamPairing struct {
	DownstreamName   string
	DownstreamBranch string
	UpstreamName     string
	UpstreamBranch   string
}
