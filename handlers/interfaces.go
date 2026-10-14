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

// Package handlers defines functionality for chisel use cases.
package handlers

import (
	"context"

	"chisel/constants"
	"chisel/contents"
	"chisel/stats"
	"chisel/types"
)

// Cache is a simple key-value store interface.
type Cache interface {
	Put(key string, value any) error
	Get(key string, out any) error
}

// GitClient provides functions to interact with a Git repository.
type GitClient interface {
	InitRepo(remoteNames, remoteURLs []string) error
	FetchBranches(MustFetchTargets, TryFetchTargets []string) error
	FirstParentCommits(commit string) ([]string, error)
	UniqueFirstParentCommits(commit string, excludeCommits []string) ([]string, error)
	UniqueFirstParentMergeCommits(commit string, excludeCommits []string) ([]string, error)
	UniqueFirstParentNoMergeCommits(commit string, excludeCommits []string) ([]string, error)
	IsRevertCommit(commit string) (bool, error)
	CommitType(commitSha1 string) (constants.CommitType, error)
	CommitInfo(commit string) (*types.CommitInfo, error)
	CommitStats(commit string) (*types.CommitStats, error)
	CommitStatsFromCombinedDiff(commit string) (*types.CommitStats, error)
	CommitDiffStats(fromCommit, toCommit string) (*types.CommitStats, error)
	UniqueReachableCommitsFromMergesOptimized(ctx context.Context, uniqueFirstParentMergeCommits, excludeCommits []string) (map[string][]string, error)
	BestMergeBase(minTargetRef, ref string) (string, error)
	RefToSha(ref string) (string, error)
	FileDiffHunkSegments(fromCommit, toCommit, file string) (*types.FileDiffHunkSegments, error)
	DiffFilesList(fromCommit, toCommit string) ([]string, error)
	LinesInFileAtCommit(commit, file string) (int, error)
	IsShallow() bool
	CommitTime(commit string) (int64, error)
	GarbageCollect() error
}

// RepoClient provides functions to interact with a Repo repository.
type RepoClient interface {
	SyncRepo(ctx context.Context) error
}

// StatsDiffer provides functions to interact with a Stats Differ.
type StatsDiffer interface {
	Analyze(ctx context.Context) (*stats.StatObjects, error)
}

// ContentsDiffer provides functions to interact with a Contents Differ.
type ContentsDiffer interface {
	Analyze(ctx context.Context) (*contents.DiffResults, error)
}

// DataSinker provides an outlet for data produced by handlers.
type DataSinker interface {
	Commit(commits *contents.CommitListItem) error
	Commits(commits []*contents.CommitListItem) error
	Files(files []string) error
	Hunk(hunks *contents.HunkListItem) error
	Hunks(hunks []*contents.HunkListItem) error
	DAG(hunks []*contents.HunkListItem) error
	LatestCommit(commit string) error
	LatestCommits(commits []types.ProjectCommit) error
}
