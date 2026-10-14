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

package contents

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"
	"chisel/types"
)

// ContentCommit groups a commit with its merge base, orderID, and resident time.
type ContentCommit struct {
	OrderID      int64
	ResidentTime int64
	types.CommitBasePair
}

// Lines after start are expected to shift by `amount`.
type shift struct {
	start          int
	amount         int
	shiftDirection ShiftDirection
}

type overlapInfo struct {
	HasOverlap   bool
	OverlapState OverlapState
	// True if the second segment fully overlaps the first.
	FullOverlap bool
}

// String converts overlapInfo into a string.
func (o overlapInfo) String() string {
	overlapState := ""
	switch o.OverlapState {
	case firstFurther:
		overlapState = "firstFurther"
	case secondFurther:
		overlapState = "secondFurther"
	case sameEnd:
		overlapState = "sameEnd"
	}
	return fmt.Sprintf("HasOverlap: %v, overlapState: %s, FullOverlap: %v", o.HasOverlap, overlapState, o.FullOverlap)
}

type fileDiffData struct {
	commitSha1           string
	fileDiffHunkSegments *types.FileDiffHunkSegments
}

type fileDiffDataHistory struct {
	prev *fileDiffData
	curr *fileDiffData
}

type shiftedBoundaries struct {
	toStarts   []int
	toEnds     []int
	fromStarts []int
	fromEnds   []int
}

// UniqueDiff uniquely identifies a diff found in a file at a specific commit.
type UniqueDiff struct {
	// Hashed contents of the other fields.
	ID int64

	CommitSha1 string
	File       string
	HunkInfo   types.HunkInfo

	// Hash of the contents.
	Hash int64
}

// String converts UniqueDiff to string.
func (u *UniqueDiff) String() string {
	return fmt.Sprintf("ID: %d, File: %s, Hash: %d, HunkInfo: %v, CommitSha1: %s", u.ID, u.File, u.Hash, &u.HunkInfo, u.CommitSha1)
}

func (u *UniqueDiff) updateID() error {
	b, err := json.Marshal(struct {
		File       string
		Hash       int64
		HunkInfo   types.HunkInfo
		CommitSha1 string
	}{
		File:       u.File,
		Hash:       u.Hash,
		HunkInfo:   u.HunkInfo,
		CommitSha1: u.CommitSha1,
	})
	if err != nil {
		return err
	}

	u.ID = int64(xxhash.Sum64(b))
	return nil
}

// DeadInfo is info related to a hunk that has been evicted from the
// liveHunkSegments.
type DeadInfo struct {
	// Current State of the dead segment.
	State DeadInfoState

	// UniqueDiff is associated with the time the hunk was first created
	// and came to life.
	UniqueDiff *UniqueDiff

	// When State == Succeeded, this is the UniqueDiff of successor
	// hunks.  There can be multiple successors in the event of
	// overlapping diffs.
	Successors []*UniqueDiff

	// This is the SHA where the hunk died.
	DeadSha1 string
}

// String converts a DeadInfo to a string.
func (d *DeadInfo) String() string {
	state := DeadInfoStateString(d.State)

	asSlice := make([]string, len(d.Successors))
	for idx, s := range d.Successors {
		asSlice[idx] = s.String()
	}

	successors := strings.Join(asSlice, " ")

	return fmt.Sprintf("State: %s, UniqueDiff: %v, DeadSha1: %s, Successors: %s", state, d.UniqueDiff, d.DeadSha1, successors)
}

// LiveHunkSegmentsData is the structure used to describe live hunk segments.
// 1st dimension is the filename.
// 2nd dimension is the hashed contents of diff.
// 3rd dimension is the HunkInfo as Uint64 and is needed to identify the
// location in the file to account for duplicates.
type LiveHunkSegmentsData map[string]map[int64]map[uint64]*UniqueDiff

// DeadHunkSegmentsData is the structure used to describe dead hunk segments.
// 1st dimension is the filename.
// 2nd dimension is the hashed contents of diff.
// 3rd dimension is the HunkInfo as Uint64 and is needed to identify the
// location in the file to account for duplicates.
// 4th dimension is the CommitSha1 of the hunk when it was live and is used to
// account for the case where a diff mutates but later returns to a prior
// version.
// 5th dimension is the DeadInfo.  It may describe a segment present in
// deadHunkSegments or liveHunkSegments.
type DeadHunkSegmentsData map[string]map[int64]map[uint64]map[string]*DeadInfo

// DiffResults capture the state of live and dead hunkSegments.
// Only returns delta results encountered on this run between
// `DifferOpts.ProjectConfig.Since` and now.
type DiffResults struct {
	Commits          []ContentCommit
	HashToContent    map[int64][]string
	LiveHunkSegments LiveHunkSegmentsData
	DeadHunkSegments DeadHunkSegmentsData
	// Set to true after Analyze() if all delta commits have been analyzed.
	AllCommitsConsumed bool
}

// DifferOpts are options to configure Differ.
type DifferOpts struct {
	ProjectConfig *types.ProjectConfig

	// This cache is for performance reasons, not functional correctness.
	Cache Cache

	GitClient GitClient

	MaxWorkers int

	// Maximum number of first-parent commits to analyze. A value <= 0 is
	// treated as unbounded.
	MaxCommits int

	// Configure the Differ with LiveHunks up to
	// `DifferOpts.ProjectConfig.Since`.
	MostRecentLiveHunks LiveHunkSegmentsData

	// Only analyze diff contents for these files.
	// If empty, all files are analyzed.
	Files []string
}

// HunkListItem is the datatype returned by `chisel list hunks`.
type HunkListItem struct {
	ID           int64
	CommitSha1   string
	MergeBase    string
	ResidentTime string
	Path         string
	Start        int
	Count        int
	SegmentType  string
	ContentHash  int64
	IsLive       bool
	DeadState    string
	DeadSha1     string
	Successors   []int64
	Content      string
}

// CommitListItem is the datatype returned by `chisel list commits`.
type CommitListItem struct {
	CommitSha1   string
	MergeBase    string
	ResidentTime string
	OrderID      int64
}
