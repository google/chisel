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
	"chisel/constants"
	"chisel/types"
)

// Commit describes data associated with a single git commit, independent of
// branch.
type Commit struct {
	types.CommitInfo

	// Git Commit SHA.
	CommitSha1 string

	// Merge, Revert, etc...
	CommitType constants.CommitType

	// FilesChanged, Insertions, and Deletions come from `git show` output
	// and serve as branch agnostic data.  Merges are treated specially; see
	// GitClient.CommitStatsFromCombinedDiff().
	FilesChanged int
	Insertions   int
	Deletions    int
}

// BranchCommit describes commit data relevant to a particular branch.
type BranchCommit struct {
	// Git Commit SHA.
	CommitSha1 string

	// True if the commit is on the first parent chain of `Branch`.
	IsFirstParent bool

	// Merge base of `CommitSha` and the upstream branch.
	MergeBase string

	// For Non-IsFirstParent commits, this is the oldest descendant on
	// `Branch`'s first-parent chain.  This is the merge commit that
	// brought `CommitSha1` into `Branch`.
	OldestDescendant string

	// For IsFirstParent commits, this is the same as CommitTime.  For
	// Non-IsFirstParent commits, this is the `OldestDescendant`'s
	// CommitTime.
	ResidentTime int64

	// For IsFirstParent commits, this is the order in the first parent chain.
	// For Non-IsFirstParent commits, this is the zero value.
	OrderID int64

	// NetFilesChanged, NetInsertions, and NetDeletions are the deltas
	// between `CommitSha1` and the upstream branch.
	NetFilesChanged int
	NetInsertions   int
	NetDeletions    int
}

// File describes file data associated with a specific commit.
type File struct {
	Path       string
	CommitSha1 string
	Insertions int
	Deletions  int
}

// BranchFile describes file data relevant to a particular branch.
type BranchFile struct {
	Path       string
	CommitSha1 string

	// NetInsertions, and NetDeletions are the deltas
	// between `CommitSha1` and the upstream branch.
	NetInsertions int
	NetDeletions  int

	// Number of lines in the upstream version of the file.
	NumLinesInFile int
}

// CommitObjects holds Commit-type data structures.
type CommitObjects struct {
	// For typical single project use, Commits can be indexed by CommitSha1.
	Commits map[string]*Commit

	// For typical single project use, BranchCommits can be indexed by
	// CommitSha1.
	BranchCommits map[string]*BranchCommit
}

// FileObjects holds File-type data structures.
type FileObjects struct {
	// For typical single project use, Files can be indexed by CommitSha1.
	Files map[string]*[]*File

	// For typical single project use, BranchFiles can be indexed by
	// CommitSha1.
	BranchFiles map[string]*[]*BranchFile
}

// StatObjects holds pointers to Commit-type and File-type structures.
type StatObjects struct {
	CommitObjects *CommitObjects
	FileObjects   *FileObjects
	// Set to true after Analyze() if all delta commits have been analyzed.
	AllCommitsConsumed bool
}

// DifferOpts are options to configure Differ.
type DifferOpts struct {
	ProjectConfig *types.ProjectConfig

	// This cache is for performance reasons, not functional correctness.
	Cache Cache

	GitClient GitClient

	// Each file's most recent BranchFile up to SinceCommit.
	// Indexed by file path.
	MostRecentBranchFiles map[string]*BranchFile

	// Commits to skip analyzing for branch agnostic commit stats.
	SkipCommits map[string]bool

	// Function to change commit emails.
	// Must be safe for concurrent calls.
	EmailMapper func(email string) (string, error)

	// Maximum number of concurrent goroutines.
	MaxWorkers int

	// Maximum number of first-parent commits to analyze. A value <= 0 is
	// treated as unbounded.
	MaxCommits int
}
