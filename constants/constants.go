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

// Package constants provides common constants used by Chisel.
package constants

// Version is the application version.
const Version = "v7.0.0-pub"

// BootstrapValue can be assigned to the `ProjectConfig.Since` Commit value
// to analyze all commits on the first parent chain.
const BootstrapValue = "BOOTSTRAP"

// NumFetchRetries is the number times to retry a git fetch command on a
// failure.
const NumFetchRetries = 5

// CommitType holds pseudo-enumerated constants for commit types.
type CommitType int

// The enumerated values for CommitType.
const (
	CommitTypeUnset CommitType = iota
	CommitTypeMerge
	CommitTypeRevert
	CommitTypeStandard
)

func CommitTypeString(commitType CommitType) string {
	switch commitType {
	case CommitTypeUnset:
		return "UNSET"
	case CommitTypeMerge:
		return "MERGE"
	case CommitTypeRevert:
		return "REVERT"
	case CommitTypeStandard:
		return "STANDARD"
	default:
		return ""
	}
}

// HunkSegmentType holds pseudo-enumerated constants for unified diff hunk
// segment types.
type HunkSegmentType int

// The enumerated values for HunkSegmentType.
const (
	UnsetHunkSegment HunkSegmentType = iota
	FromHunkSegment
	ToHunkSegment
)

// HunkSegmentTypeString emulates a constant map of int to string.
func HunkSegmentTypeString(segmentType HunkSegmentType) string {
	switch segmentType {
	case FromHunkSegment:
		return "From"
	case ToHunkSegment:
		return "To"
	default:
		return ""
	}
}

// URL parameters.
const (
	DownstreamHostNameParam = "DownstreamHostName"
	DownstreamBranchParam   = "DownstreamBranch"
	UpstreamHostNameParam   = "UpstreamHostName"
	UpstreamBranchParam     = "UpstreamBranch"
	ProjectParam            = "Project"
	BeforeCommitParam       = "BeforeCommit"
	CommitParam             = "Commit"
	FileParam               = "File"
	HunkIDParam             = "HunkID"
	LineParam               = "Line"
)

// Endpoints.
const (
	CommitEndpoint  = "commit"
	CommitsEndpoint = "commits"
	FilesEndpoint   = "files"
	HunkEndpoint    = "hunk"
	HunksEndpoint   = "hunks"
)

// 'list' actions.
const (
	UnsetAction           = "unset"
	CommitAction          = "commit"
	CommitsAction         = "commits"
	FilesAction           = "files"
	HunksAction           = "hunks"
	HunkAction            = "hunk"
	DAGAction             = "dag"
	LatestContentsCommit  = "latest_contents_commit"
	LatestStatsCommit     = "latest_stats_commit"
	LatestContentsCommits = "latest_contents_commits"
	LatestStatsCommits    = "latest_stats_commits"
)


// Directories inside the chisel work directory.
const (
	SqliteDir  = "sqlite"
	CacheDir   = "cache"
	ReposDir   = "repos"
	GrafanaDir = "grafana"
)

// DefaultConfigFile is the name of the config file Chisel searches for.
const DefaultConfigFile = "chisel.yaml"

// SqliteDBFile is the name of the created db file.
const SqliteDBFile = "chisel.db"
