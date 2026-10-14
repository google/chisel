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

package stats

import (
	"context"

	"chisel/constants"
	"chisel/types"
)

// Cache is a simple key-value store interface.
type Cache interface {
	Put(key string, value any) error
	Get(key string, out any) error
}

// GitClient provides functions to interact with a Git repository.
type GitClient interface {
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
}
