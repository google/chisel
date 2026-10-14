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

// Package contents provides insight into how diff contents change over time.
package contents

import (
	"os"
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"log"
	"github.com/cespare/xxhash/v2"
	"chisel/commits"
	"chisel/constants"
	"chisel/types"
)

// DiffResultsAction can adds uniqueness to a Project Key hash.
const DiffResultsAction = "DiffResults"

func abs(i int) int {
	if i < 0 {
		return i * -1
	}
	return i
}

// Differ collects content diffs for a single project.
type Differ struct {
	opts     *DifferOpts
	position int

	downstreamHeadCommit string
	keyPrefix            string

	uniqueFirstParentCommits []string
	mergeBases               []string
	residentTime             []int64

	// Index is the file name.
	fileDiffDataHistory map[string]*fileDiffDataHistory

	// Maps the hashed value of a hunk to its content.
	hashToContent map[int64][]string

	liveHunkSegments       LiveHunkSegmentsData
	stagedLiveHunkSegments LiveHunkSegmentsData

	deadHunkSegments DeadHunkSegmentsData

	areCommitsPending bool

	// Only analyze files in this set.
	filesMap map[string]bool
}

// New creates a Differ and does some initial setup checking.
func New(opts *DifferOpts) (*Differ, error) {
	if opts.GitClient == nil {
		return nil, errors.New("received nil GitClient")
	}

	filesMap := make(map[string]bool)
	for _, f := range opts.Files {
		filesMap[f] = true
	}

	differ := &Differ{
		opts:                   opts,
		position:               -1,
		fileDiffDataHistory:    make(map[string]*fileDiffDataHistory),
		hashToContent:          make(map[int64][]string),
		liveHunkSegments:       make(map[string]map[int64]map[uint64]*UniqueDiff),
		stagedLiveHunkSegments: make(map[string]map[int64]map[uint64]*UniqueDiff),
		deadHunkSegments:       make(map[string]map[int64]map[uint64]map[string]*DeadInfo),
		filesMap:               filesMap,
	}

	return differ, nil
}

func (d *Differ) analyzeRangeInfo() string {
	var sb strings.Builder
	length := len(d.uniqueFirstParentCommits)

	fmt.Fprintf(&sb, "project: %s", d.projectName())
	fmt.Fprintf(&sb, " startCommit: %s", d.uniqueFirstParentCommits[0])
	fmt.Fprintf(&sb, " startCommitOrderId: %d", d.opts.ProjectConfig.Since.NextStartID())
	fmt.Fprintf(&sb, " endCommit: %s", d.uniqueFirstParentCommits[length-1])
	fmt.Fprintf(&sb, " endCommitOrderId: %d", d.opts.ProjectConfig.Since.NextStartID()+int64(length-1))

	return sb.String()
}

// Analyze steps through all first parent commits and returns changes
// over time.
func (d *Differ) Analyze(ctx context.Context) (*DiffResults, error) {

	if err := d.checkConfig(); err != nil {
		return nil, err
	}

	if d.opts.GitClient.IsShallow() {
		log.Printf("Skipping shallow project %v", d.opts.ProjectConfig)
		return nil, nil
	}

	if err := d.setup(ctx); err != nil {
		return nil, err
	}

	if len(d.uniqueFirstParentCommits) == 0 {
		log.Printf("There aren't any new commits for project %s.", d.projectName())
		return nil, nil
	}

	foundMergeBase := false
	for _, mb := range d.mergeBases {
		if mb != "" {
			foundMergeBase = true
			break
		}
	}
	if !foundMergeBase {
		log.Printf("There aren't any merge bases for project %s.", d.projectName())
		return nil, nil
	}

	if err := d.restoreFromPriorState(); err != nil {
		return nil, err
	}

	if err := d.createKeyPrefix(); err != nil {
		return nil, err
	}

	var diffResults *DiffResults

	rangeInfo := d.analyzeRangeInfo()
	log.Printf("Calling stepUntilDone for %s", rangeInfo)
	key := d.keyPrefix + "_stepUntilDone"
	err := d.cacheGet(key, &diffResults)
	if err != nil {
		if diffResults, err = d.stepUntilDone(ctx); err != nil {
			return nil, fmt.Errorf("stepUntilDone for %s : %w", rangeInfo, err)
		}

		if err = d.cachePut(key, &diffResults); err != nil {
			return nil, fmt.Errorf("unable to add diffResults to cache for %s : %w", rangeInfo, err)
		}
	} else {
		// Local state must be made consistent with cached values before being
		// reconciled.
		d.hashToContent = diffResults.HashToContent
		d.liveHunkSegments = diffResults.LiveHunkSegments
		d.deadHunkSegments = diffResults.DeadHunkSegments
	}

	log.Printf("Reconciling hunk duplicates for project %s...", d.projectName())
	d.reconcileLiveHunkDuplicates()

	return diffResults, nil
}

func (d *Differ) projectName() string {
	return d.opts.ProjectConfig.Name
}

func (d *Differ) cachePut(key string, value any) error {
	// Not viewing a nil cache as an error.  It's just a loss
	// of performance.
	if d.opts.Cache == nil {
		return nil
	}
	return d.opts.Cache.Put(key, value)
}

func (d *Differ) cacheGet(key string, out any) error {
	// Treat a nil cache as a miss.
	if d.opts.Cache == nil {
		return errors.New("nil cache")
	}
	return d.opts.Cache.Get(key, out)
}

// Deep copy the mostRecentLiveHunks.
func (d *Differ) restoreFromPriorState() error {
	if d.opts.MostRecentLiveHunks == nil {
		return nil
	}

	liveHunkSegments := make(map[string]map[int64]map[uint64]*UniqueDiff)

	for file, m1 := range d.opts.MostRecentLiveHunks {
		for hash, m2 := range m1 {
			for hunkInfo, uniqueDiff := range m2 {

				mHash, exists := liveHunkSegments[file]
				if !exists {
					mHash = make(map[int64]map[uint64]*UniqueDiff)
					liveHunkSegments[file] = mHash
				}

				mHunk, exists := mHash[hash]
				if !exists {
					mHunk = make(map[uint64]*UniqueDiff)
					mHash[hash] = mHunk
				}
				mHunk[hunkInfo] = uniqueDiff
			}
		}
		if err := d.gitDiffAndUpdateHistory(d.opts.ProjectConfig.Since.MergeBase, d.opts.ProjectConfig.Since.Commit, file); err != nil {
			return err
		}
	}
	d.liveHunkSegments = liveHunkSegments

	return nil
}

func (d *Differ) setup(ctx context.Context) error {
	maxWorkersResolved := max(1, d.opts.MaxWorkers)
	excludeRefs := []string{d.opts.ProjectConfig.UpstreamRef()}
	if d.opts.ProjectConfig.Since.Commit != constants.BootstrapValue && d.opts.ProjectConfig.Since.Commit != "" {
		excludeRefs = append(excludeRefs, d.opts.ProjectConfig.Since.Commit)
	}

	var err error
	log.Printf("Calling UniqueFirstParentCommits for project %s...", d.projectName())
	uOpts := &commits.UniqueFirstParentCommitsOptions{
		Ref:         d.opts.ProjectConfig.DownstreamRef(),
		ExcludeRefs: excludeRefs,
		Cache:       d.opts.Cache,
		GitClient:   d.opts.GitClient,
	}
	if d.uniqueFirstParentCommits, err = commits.UniqueFirstParentCommits(uOpts); err != nil {
		return fmt.Errorf("UniqueFirstParentCommits for project %s : %w", d.projectName(), err)
	}
	if d.opts.MaxCommits > 0 {
		end := min(len(d.uniqueFirstParentCommits), d.opts.MaxCommits)
		d.areCommitsPending = end < len(d.uniqueFirstParentCommits)
		d.uniqueFirstParentCommits = d.uniqueFirstParentCommits[:end]
	}

	log.Printf("Calling MergeBases for project %s...", d.projectName())
	mOpts := &commits.MergeBasesOptions{
		UniqueFirstParentCommits: d.uniqueFirstParentCommits,
		UpstreamRef:              d.opts.ProjectConfig.UpstreamRef(),
		MaxWorkers:               maxWorkersResolved,
		Cache:                    d.opts.Cache,
		GitClient:                d.opts.GitClient,
	}
	if d.mergeBases, err = commits.MergeBases(ctx, mOpts); err != nil {
		return fmt.Errorf("MergeBases for project %s : %w", d.projectName(), err)
	}

	d.residentTime = make([]int64, len(d.uniqueFirstParentCommits))

	return nil
}

// Does not do error checking on presence of item in liveHunkSegments.
func (d *Differ) deleteLiveHunkSegment(file string, hash int64, hunkInfo types.HunkInfo) {
	hunkInfoUint64 := hunkInfo.ToUint64()

	mHash := d.liveHunkSegments[file]
	mHunk := mHash[hash]
	delete(mHunk, hunkInfoUint64)
	if len(mHunk) == 0 {
		delete(mHash, hash)
	}
	if len(mHash) == 0 {
		delete(d.liveHunkSegments, file)
	}
}

func (d *Differ) insertLiveHunkSegmentsData(liveHunkSegments LiveHunkSegmentsData, file, commit string, hunkSegment *types.HunkSegment) (*UniqueDiff, error) {
	mHash, exists := liveHunkSegments[file]
	if !exists {
		mHash = make(map[int64]map[uint64]*UniqueDiff)
		liveHunkSegments[file] = mHash
	}

	mHunk, exists := mHash[hunkSegment.Hash]
	if !exists {
		mHunk = make(map[uint64]*UniqueDiff)
		mHash[hunkSegment.Hash] = mHunk
	}

	d.hashToContent[hunkSegment.Hash] = hunkSegment.Content

	uniqueDiff := &UniqueDiff{
		File:       file,
		Hash:       hunkSegment.Hash,
		HunkInfo:   hunkSegment.HunkInfo,
		CommitSha1: commit,
	}
	if err := uniqueDiff.updateID(); err != nil {
		return nil, err
	}

	if os.Getenv("CHISEL_DEBUG") == "2" {
		log.Printf("Inserting liveHunkSegment file: %s commit: %s hash: %d hunkInfo: %v", file, commit, hunkSegment.Hash, &hunkSegment.HunkInfo)
	}
	mHunk[hunkSegment.HunkInfo.ToUint64()] = uniqueDiff

	return uniqueDiff, nil
}

// This bypasses staging.
func (d *Differ) insertLiveHunkSegment(file, commit string, hunkSegment *types.HunkSegment) (*UniqueDiff, error) {
	return d.insertLiveHunkSegmentsData(d.liveHunkSegments, file, commit, hunkSegment)
}

// In order to prevent the scenario where a liveHunkSegment is added and then
// deleted during a single commit analysis, liveHunkSegment insertions
// need to be staged in a temporary area. This can happen when the same
// composite key (file, hash, hunkInfo) is found in back-to-back commits.
// Without staging, the 2nd commit will override the hunk inserted by the 1st
// commit. Then, if that (file, hash, hunkInfo) is succeeded, the hunk from
// the 2nd commit will be the one marked for death, rather than the hunk from
// the 1st commit.
func (d *Differ) insertStagedLiveHunkSegment(file, commit string, hunkSegment *types.HunkSegment) (*UniqueDiff, error) {
	return d.insertLiveHunkSegmentsData(d.stagedLiveHunkSegments, file, commit, hunkSegment)
}

func (d *Differ) commitStagedLiveHunks(file, commit string) error {
	mHashStaged, exists := d.stagedLiveHunkSegments[file]
	if !exists {
		return nil
	}

	mHash, exists := d.liveHunkSegments[file]
	if !exists {
		mHash = make(map[int64]map[uint64]*UniqueDiff)
		d.liveHunkSegments[file] = mHash
	}

	for hash, m1 := range mHashStaged {
		mHunk, exists := mHash[hash]
		if !exists {
			mHunk = make(map[uint64]*UniqueDiff)
			mHash[hash] = mHunk
		}

		for hunkInfo, uniqueDiff := range m1 {
			mHunk[hunkInfo] = uniqueDiff
		}
	}
	delete(d.stagedLiveHunkSegments, file)

	return nil
}

func (d *Differ) insertDeadHunkSegments(file string, hash int64, hunkInfo types.HunkInfo, commitSha1 string, deadInfo *DeadInfo) error {
	mHash, exists := d.deadHunkSegments[file]
	if !exists {
		mHash = make(map[int64]map[uint64]map[string]*DeadInfo)
		d.deadHunkSegments[file] = mHash
	}

	mHunk, exists := mHash[hash]
	if !exists {
		mHunk = make(map[uint64]map[string]*DeadInfo)
		mHash[hash] = mHunk
	}

	mCommit, exists := mHunk[hunkInfo.ToUint64()]
	if !exists {
		mCommit = make(map[string]*DeadInfo)
		mHunk[hunkInfo.ToUint64()] = mCommit
	}

	if _, exists = mCommit[commitSha1]; exists {
		return fmt.Errorf("found an already existing deadHunk.  file: %s hash: %d, hunkInfo: %v, commitSha1: %s", file, hash, &hunkInfo, commitSha1)
	}

	if os.Getenv("CHISEL_DEBUG") == "2" {
		log.Printf("Inserting deadHunkSegment file: %s hash: %d hunkInfo: %v commitSha1: %s deadInfo: %v", file, hash, &hunkInfo, commitSha1, deadInfo)
	}
	mCommit[commitSha1] = deadInfo
	return nil
}

func (d *Differ) retireLiveHunk(file string, hash int64, hunkInfo types.HunkInfo, commitSha1 string) error {
	mHash, exists := d.liveHunkSegments[file]
	if !exists {
		return fmt.Errorf("couldn't find [%s] in liveHunkSegments on a retire", file)
	}

	mHunk, exists := mHash[hash]
	if !exists {
		return fmt.Errorf("couldn't find [%s][%d] in liveHunkSegments on a retire", file, hash)
	}

	uniqueDiff, exists := mHunk[hunkInfo.ToUint64()]
	if !exists {
		return fmt.Errorf("couldn't find [%s][%d][%v] in liveHunkSegments on a retire", file, hash, &hunkInfo)
	}

	deadInfo := &DeadInfo{
		State:      DeadStateRetired,
		DeadSha1:   commitSha1,
		UniqueDiff: uniqueDiff,
	}
	if err := d.insertDeadHunkSegments(file, hash, hunkInfo, uniqueDiff.CommitSha1, deadInfo); err != nil {
		return err
	}
	d.deleteLiveHunkSegment(file, hash, hunkInfo)

	return nil
}

func (d *Differ) searchLiveHunkSegment(file string, hash int64, hunkInfo types.HunkInfo) (*UniqueDiff, bool) {
	mHash, exists := d.liveHunkSegments[file]
	if !exists {
		return nil, false
	}

	mHunk, exists := mHash[hash]
	if !exists {
		return nil, false
	}

	val, exists := mHunk[hunkInfo.ToUint64()]

	return val, exists
}

func (d *Differ) updateLiveHunkEntryAndStage(file, commit string, prevHunkSegment *types.HunkSegment, currHunkSegments ...*types.HunkSegment) error {
	mHash, exists := d.liveHunkSegments[file]
	if !exists {
		return fmt.Errorf("couldn't find [%s] in liveHunkSegments on an update", file)
	}

	prevHash := prevHunkSegment.Hash
	mHunk, exists := mHash[prevHash]
	if !exists {
		return fmt.Errorf("couldn't find [%s][%d] in liveHunkSegments on an update", file, prevHash)
	}
	if os.Getenv("CHISEL_DEBUG") == "2" {
		var sb strings.Builder
		for idx, h := range currHunkSegments {
			s := fmt.Sprintf("currHunkInfo[%d] = %v ", idx, &h.HunkInfo)
			sb.WriteString(s)

		}
		currHunkInfoString := sb.String()
		if len(currHunkInfoString) > 0 {
			currHunkInfoString = currHunkInfoString[:len(currHunkInfoString)-1]
		}

		log.Printf("updateLiveHunkEntryAndStage file: %s commit: %s prevHunkInfo: %v currHunkInfo: %s", file, commit, &prevHunkSegment.HunkInfo, currHunkInfoString)
	}

	successors := make([]*UniqueDiff, len(currHunkSegments))
	for idx, currHunkSegment := range currHunkSegments {
		currUniqueDiff, err := d.insertStagedLiveHunkSegment(file, commit, currHunkSegment)
		if err != nil {
			return err
		}
		successors[idx] = currUniqueDiff
	}

	// Previous live hunks can be retired at this point now that a successor
	// exists.
	prevUniqueDiff, exists := mHunk[prevHunkSegment.HunkInfo.ToUint64()]
	if !exists {
		return fmt.Errorf("couldn't find liveHunkSegment for file: %s, prevHash: %d, prevHunkInfo: %v", file, prevHash, &prevHunkSegment.HunkInfo)
	}

	d.deleteLiveHunkSegment(file, prevHash, prevHunkSegment.HunkInfo)

	deadInfo := &DeadInfo{
		State:      DeadStateSucceeded,
		Successors: successors,
		DeadSha1:   commit,
		UniqueDiff: prevUniqueDiff,
	}

	return d.insertDeadHunkSegments(file, prevHash, prevHunkSegment.HunkInfo, prevUniqueDiff.CommitSha1, deadInfo)
}

func (d *Differ) gitDiffAndUpdateHistory(mergeBase, commit, file string) error {
	fileDiffHunkSegments, err := d.opts.GitClient.FileDiffHunkSegments(mergeBase, commit, file)
	if err != nil {
		return fmt.Errorf("unable to get file diffs between %s and %s for %s: %v", mergeBase, commit, file, err)
	}

	fDH, exists := d.fileDiffDataHistory[file]
	if !exists {
		d.fileDiffDataHistory[file] = &fileDiffDataHistory{
			curr: &fileDiffData{
				commitSha1:           commit,
				fileDiffHunkSegments: fileDiffHunkSegments,
			},
		}
	} else {
		if fDH.prev == nil {
			fDH.prev = &fileDiffData{}
		}
		*(fDH.prev) = *(fDH.curr)
		fDH.curr.commitSha1 = commit
		fDH.curr.fileDiffHunkSegments = fileDiffHunkSegments
	}

	return nil
}

// We want the shifts slice to be monotonically increasing and without
// `Start` duplicates. There are two situations where this property is
// violated and needs to be fixed.
// 1) A modification of a line shows up as an insertion and deletion with same
// `Start`.
// 2) The manual adjustments done to 'FromHunkSegments' in 'shifts()' may not
// result in correct placement in the shifts slice.
func maybeFixupShifts(shifts []shift, delta int) ([]shift, error) {
	// There's nothing to fixup with 0 or 1 shifts.
	if len(shifts) < 2 {
		return shifts, nil
	}

	lastIdx := len(shifts) - 1
	// If the last entry in shifts already preserves the monotonically
	// increasing property, then there's nothing to fix.
	if shifts[lastIdx].start > shifts[lastIdx-1].start {
		return shifts, nil
	}

	last := shifts[lastIdx]

	insertedIdx := -1
	// Loop count does not include index 0 as that is the sentinel
	// initial shift.
	for idx := lastIdx - 1; idx > 0; idx-- {
		if shifts[idx].start == last.start {
			// Duplicate found. Combine the shifts and delete the last item.
			tgt := &shifts[idx]
			tgt.amount += delta

			prevAmount := shifts[idx-1].amount
			if tgt.amount > prevAmount {
				tgt.shiftDirection = insertion
			} else if tgt.amount < prevAmount {
				tgt.shiftDirection = deletion
			} else {
				tgt.shiftDirection = offsetting
			}

			shifts = shifts[:len(shifts)-1]
			insertedIdx = idx

			break
		} else if shifts[idx].start < last.start {
			// New insertion location found. Need to move the tail into the
			// new position and then chop the tail.
			shifts = append(shifts[:idx+1], shifts[idx:len(shifts)-1]...)
			shifts[idx+1] = last
			shifts[idx+1].amount = shifts[idx].amount + delta

			insertedIdx = idx + 1
			break
		}
	}

	if insertedIdx == -1 {
		return nil, fmt.Errorf("couldn't find new place to insert shift: %v shifts: %v", last, shifts)
	}

	// Apply the newly-moved delta to every shift to the right.
	for i := insertedIdx + 1; i < len(shifts); i++ {
		shifts[i].amount += delta
	}
	return shifts, nil
}

// Finds the amount of shift that will be applied to line ranges.
// Use case is to predict the how to map lines present in a previous version
// of a file to lines present in a newer version.
func (d *Differ) shifts(file, commit1, commit2 string) ([]shift, error) {
	diffHunkSegments, err := d.opts.GitClient.FileDiffHunkSegments(commit1, commit2, file)
	if err != nil {
		return nil, fmt.Errorf("unable to get file diffs between %s and %s for %s: %v", commit1, commit2, file, err)
	}

	shifts := []shift{
		shift{start: 0, amount: 0},
	}
	runningOffset := 0
	startAdjustment := 0
	for _, segment := range diffHunkSegments.SortedHunkSegments {
		delta := segment.Count
		shiftDirection := insertion
		startAdjustment = 0
		if segment.SegmentType == constants.FromHunkSegment {
			delta *= -1
			shiftDirection = deletion
			// Important:
			// When inspecting the diffs of a file,
			// later hunk insertions `start` are impacted by the total
			// runningOffset from previous hunk insertions.
			// Later hunk deletions `start` however are not, and need to be
			// adjusted manually.
			/*
				e.g. The deletion in this snippet shows it occurring at line 18,
				but that is relative to previous version of the file.  For the use
				case of shifts(), the applied -1 shift, which must take into account the
				+3 shift from line 17, occurs at line 21.
				@@ -16,0 +17,3 @@
				+line1
				+line2
				+line3
				@@ -18 +21,3 @@ package android.uirendering.cts.testclasses;
				-
				+line4
				+line5
				+line6
			*/
			startAdjustment = runningOffset
		}
		runningOffset += delta

		newStart := segment.Start + startAdjustment
		shifts = append(shifts, shift{newStart, runningOffset, shiftDirection})
		if shifts, err = maybeFixupShifts(shifts, delta); err != nil {
			return nil, err
		}
	}

	return shifts, nil
}

func (d *Differ) shiftsFromAllEdits(file, commit string) ([]shift, error) {
	fileDiffHistory, exists := d.fileDiffDataHistory[file]
	if !exists {
		return nil, fmt.Errorf("unable to find file history for %s", file)
	}

	prevHistory := fileDiffHistory.prev
	if prevHistory == nil {
		return nil, fmt.Errorf("unable to find previous file history for %s", file)
	}

	shifts, err := d.shifts(file, prevHistory.commitSha1, commit)
	if err != nil {
		return nil, fmt.Errorf("shifts: prevHistory.commitSha1: %s commit: %s : %v", prevHistory.commitSha1, commit, err)
	}

	return shifts, nil
}

func (d *Differ) shiftsFromUpstreamEdits(file string) ([]shift, error) {
	curr := d.mergeBases[d.position]
	var prev string
	if d.position == 0 {
		if d.opts.ProjectConfig.Since.MergeBase == "" {
			return nil, fmt.Errorf("no prior version to diff against to find shifts for file: %s", file)
		}
		prev = d.opts.ProjectConfig.Since.MergeBase
	} else {
		prev = d.mergeBases[d.position-1]
	}

	shifts, err := d.shifts(file, prev, curr)
	if err != nil {
		return nil, fmt.Errorf("shifts: prev: %s curr: %s : %v", prev, curr, err)
	}

	return shifts, nil
}

// This bypasses staging.
func (d *Differ) insertAllCurrentHunkSegments(file, commit string) error {
	for _, currHunkSegment := range d.fileDiffDataHistory[file].curr.fileDiffHunkSegments.SortedHunkSegments {
		if _, err := d.insertLiveHunkSegment(file, commit, currHunkSegment); err != nil {
			return err
		}
	}
	return nil
}

func (d *Differ) liveHunksNotInDiffFilesList(diffFilesList []string) []string {
	diffFilesMap := make(map[string]bool)
	for _, f := range diffFilesList {
		diffFilesMap[f] = true
	}

	convergedList := []string{}
	for liveHunkFile := range d.liveHunkSegments {
		if _, exists := diffFilesMap[liveHunkFile]; !exists {
			convergedList = append(convergedList, liveHunkFile)
		}
	}
	return convergedList
}

func (d *Differ) retireAllHunksConvergedFiles(commit string, diffFilesList []string) error {
	convergedList := d.liveHunksNotInDiffFilesList(diffFilesList)

	for _, file := range convergedList {
		if os.Getenv("CHISEL_DEBUG") == "2" {
			log.Printf("file: %s converged at commit: %s", file, commit)
		}
		for hash, m1 := range d.liveHunkSegments[file] {
			for hunkInfo := range m1 {
				h := types.HunkInfo{}
				h.FromUint64(hunkInfo)
				if err := d.retireLiveHunk(file, hash, h, commit); err != nil {
					return nil
				}
			}
		}
		delete(d.fileDiffDataHistory, file)
	}

	return nil
}

// Returns shift amount and the index in shifts[] where hunkSegment settled.
// Positive delta shifts need to be continuously applied until two applications
// end in the same shift band.
func shiftAmountForSegment(shiftsAllEdits, shiftsUpstreamEdits []shift, startIdx int, hunkSegment *types.HunkSegment, boundary BoundaryType) (int, int) {
	var shifts []shift

	// FromHunks (deletions) are only impacted by movement in upstream code.
	if hunkSegment.SegmentType == constants.FromHunkSegment {
		shifts = shiftsUpstreamEdits
	} else {
		shifts = shiftsAllEdits
	}

	// If only the `initial` shift is present, then
	// no shift is required
	if len(shifts) == 1 {
		return 0, 0
	}

	var bound int
	if boundary == start {
		bound = hunkSegment.Start
	} else {
		bound = hunkSegment.Start + hunkSegment.Count - 1
	}

	// running shift count is used for delta calculation.
	runningCount := 0

	// Last index is the default case.
	lastIdx := len(shifts) - 1
	foundIdx := lastIdx
	prevFoundIdx := -1

	// firstFoundIdx marks the first band where a segment attempts to settle.
	// It can be used as the start iteration on successive hunkSegments as
	// a performance optimization. Otherwise, starting at 0 is functionally
	// correct.
	firstFoundIdx := -1

	// Loop until hunk settles in a range.
	settleAttempt := 1
	for {
		foundIdx = lastIdx

		for i := startIdx; i <= lastIdx; i++ {
			if shifts[i].start <= bound {
				continue
			}

			if i == 0 {
				// The first index in shifts[] is a placeholder.
				return 0, 0
			}

			foundIdx = i - 1
			if firstFoundIdx == -1 {
				firstFoundIdx = foundIdx
			}
			break
		}

		delta := shifts[foundIdx].amount - runningCount
		// If we encounter a negative delta, we're done.
		if delta < 0 {
			break
		}

		// Can stop seeking once we've settled.
		if prevFoundIdx == foundIdx {
			break
		}
		prevFoundIdx = foundIdx
		runningCount = shifts[foundIdx].amount
		bound += delta
		settleAttempt++
	}

	boundAdjustment := 0
	// This is quite ugly but reached this by extensive testing.
	if boundary == end {
		boundAdjustment = 1
	}
	shiftAmount := max(shifts[foundIdx].amount, shifts[foundIdx].start-bound+runningCount-boundAdjustment)
	return shiftAmount, firstFoundIdx
}

// Returns the new boundary where the hunk will be in the file after applying shifts.
// Note that if the hunk was mutated, it won't line up perfectly.
func newBoundariesForSegments(shiftsAllEdits, shiftsUpstreamEdits []shift, sortedHunkSegments []*types.HunkSegment, boundary BoundaryType, segmentType constants.HunkSegmentType) ([]int, error) {
	firstFoundIdx := 0
	shiftAmount := 0
	startIdx := 0

	newBoundary := []int{}

	for _, hunkSegment := range sortedHunkSegments {
		if hunkSegment.SegmentType != segmentType {
			continue
		}

		shiftAmount, firstFoundIdx = shiftAmountForSegment(shiftsAllEdits, shiftsUpstreamEdits, startIdx, hunkSegment, boundary)

		if boundary == start {
			newBoundary = append(newBoundary, hunkSegment.Start+shiftAmount)
		} else {
			newBoundary = append(newBoundary, hunkSegment.Start+hunkSegment.Count-1+shiftAmount)
		}

		// Start the next iteration at the previous index.
		// This forces correct running count and delta math.
		startIdx = max(firstFoundIdx-1, 0)
	}

	return newBoundary, nil
}

func (d *Differ) shiftedBoundaries(file, commit string, prevSortedHunkSegments []*types.HunkSegment) (*shiftedBoundaries, error) {
	shiftsAllEdits, err := d.shiftsFromAllEdits(file, commit)
	if err != nil {
		return nil, err
	}
	shiftsUpstreamEdits, err := d.shiftsFromUpstreamEdits(file)
	if err != nil {
		return nil, err
	}

	// Find how the previous hunks would be expected to move due to
	// the shifts.
	shiftedToStarts, err := newBoundariesForSegments(shiftsAllEdits, shiftsUpstreamEdits, prevSortedHunkSegments, start, constants.ToHunkSegment)
	if err != nil {
		return nil, err
	}
	shiftedToEnds, err := newBoundariesForSegments(shiftsAllEdits, shiftsUpstreamEdits, prevSortedHunkSegments, end, constants.ToHunkSegment)
	if err != nil {
		return nil, err
	}

	shiftedFromStarts, err := newBoundariesForSegments(shiftsAllEdits, shiftsUpstreamEdits, prevSortedHunkSegments, start, constants.FromHunkSegment)
	if err != nil {
		return nil, err
	}
	shiftedFromEnds, err := newBoundariesForSegments(shiftsAllEdits, shiftsUpstreamEdits, prevSortedHunkSegments, end, constants.FromHunkSegment)
	if err != nil {
		return nil, err
	}

	if os.Getenv("CHISEL_DEBUG") == "2" {
		log.Printf("file: %s commit: %s shiftsAllEdits: %v", file, commit, shiftsAllEdits)
		log.Printf("file: %s commit: %s shiftsUpstreamEdits: %v", file, commit, shiftsUpstreamEdits)
		log.Printf("file: %s commit: %s shiftedToStarts: %v", file, commit, shiftedToStarts)
		log.Printf("file: %s commit: %s shiftedToEnds: %v", file, commit, shiftedToEnds)
		log.Printf("file: %s commit: %s shiftedFromStarts: %v", file, commit, shiftedFromStarts)
		log.Printf("file: %s commit: %s shiftedFromEnds: %v", file, commit, shiftedFromEnds)
	}

	return &shiftedBoundaries{
		shiftedToStarts,
		shiftedToEnds,
		shiftedFromStarts,
		shiftedFromEnds}, nil
}

func popShiftFront(slice *[]int) error {
	if len(*slice) == 0 {
		return errors.New("cannot pop from an empty slice")
	}

	*slice = (*slice)[1:]

	return nil
}

func checkOverlapDirectComparison(shiftedStart, shiftedEnd int, hunkSegmentFirst, hunkSegmentSecond *types.HunkSegment) overlapInfo {
	info := overlapInfo{}

	hunkSegmentFirstStart := shiftedStart
	hunkSegmentFirstEnd := shiftedEnd

	hunkSegmentSecondStart := hunkSegmentSecond.HunkInfo.Start
	hunkSegmentSecondEnd := hunkSegmentSecondStart + hunkSegmentSecond.HunkInfo.Count - 1

	if hunkSegmentFirstEnd == hunkSegmentSecondEnd {
		info.OverlapState = sameEnd
	} else if hunkSegmentFirstEnd > hunkSegmentSecondEnd {
		info.OverlapState = firstFurther
	} else {
		info.OverlapState = secondFurther
	}

	if hunkSegmentFirstStart >= hunkSegmentSecondStart &&
		hunkSegmentFirstStart <= hunkSegmentSecondEnd {
		info.HasOverlap = true
	}

	if hunkSegmentFirstEnd >= hunkSegmentSecondStart &&
		hunkSegmentFirstEnd <= hunkSegmentSecondEnd {
		info.HasOverlap = true
	}

	if hunkSegmentSecondStart >= hunkSegmentFirstStart &&
		hunkSegmentSecondStart <= hunkSegmentFirstEnd {
		info.HasOverlap = true
	}

	if hunkSegmentSecondEnd >= hunkSegmentFirstStart &&
		hunkSegmentSecondEnd <= hunkSegmentFirstEnd {
		info.HasOverlap = true
	}

	if hunkSegmentSecondStart <= hunkSegmentFirstStart &&
		hunkSegmentSecondEnd >= hunkSegmentFirstEnd {
		info.FullOverlap = true
	}

	return info
}

func checkOverlapCurrIsFromHunk(shiftedStart, shiftedEnd int, hunkSegmentFirst, hunkSegmentSecond *types.HunkSegment) overlapInfo {
	if hunkSegmentFirst.SegmentType == constants.FromHunkSegment {
		return checkOverlapDirectComparison(shiftedStart, shiftedEnd, hunkSegmentFirst, hunkSegmentSecond)
	}

	info := overlapInfo{}

	shiftedSize := shiftedEnd - shiftedStart + 1
	// If the firstHunk doesn't shift in size, then no additional lines were
	// deleted and therefore no overlap.
	if shiftedSize == hunkSegmentFirst.Count {
		info.HasOverlap = false
		info.FullOverlap = false
	} else {
		info.HasOverlap = true
		if shiftedSize < 0 {
			info.FullOverlap = true
		} else {
			info.FullOverlap = false
		}
	}

	if hunkSegmentFirst.Start > hunkSegmentSecond.Start {
		info.OverlapState = firstFurther
	} else {
		info.OverlapState = secondFurther
	}
	return info
}

func checkOverlap(shiftedStart, shiftedEnd int, hunkSegmentFirst, hunkSegmentSecond *types.HunkSegment) overlapInfo {
	if hunkSegmentSecond.SegmentType == constants.FromHunkSegment {
		return checkOverlapCurrIsFromHunk(shiftedStart, shiftedEnd, hunkSegmentFirst, hunkSegmentSecond)
	}

	return checkOverlapDirectComparison(shiftedStart, shiftedEnd, hunkSegmentFirst, hunkSegmentSecond)
}

func (d *Differ) handleHunkSegments(file, commit string, shiftedStarts, shiftedEnds []int, prevHunkSegments, currHunkSegments []*types.HunkSegment, segmentType constants.HunkSegmentType) error {
	if os.Getenv("CHISEL_DEBUG") == "3" {
		st := constants.HunkSegmentTypeString(segmentType)
		for idx, p := range prevHunkSegments {
			log.Printf("file: %s commit: %s %s: prevHunkSegments[%d]: %v", file, commit, st, idx, p)
		}
		for idx, p := range currHunkSegments {
			log.Printf("file: %s commit: %s %s: currHunkSegments[%d]: %v", file, commit, st, idx, p)
		}
	}

	prevIdx := 0
	currIdx := 0
	stepPrev := func(segmentType constants.HunkSegmentType) error {
		prevIdx++

		if err := popShiftFront(&shiftedStarts); err != nil {
			return err
		}

		return popShiftFront(&shiftedEnds)
	}

	for prevIdx < len(prevHunkSegments) || currIdx < len(currHunkSegments) {
		if os.Getenv("CHISEL_DEBUG") == "2" {
			log.Printf("file: %s commit: %s prevIdx: %d currIdx: %d", file, commit, prevIdx, currIdx)
		}
		if currIdx >= len(currHunkSegments) {
			// If there aren't anymore current hunks, then that means any
			// remaining previous hunks have retired.
			prevHunkSegment := prevHunkSegments[prevIdx]
			if err := d.retireLiveHunk(file, prevHunkSegment.Hash, prevHunkSegment.HunkInfo, commit); err != nil {
				return fmt.Errorf("failed to retireLiveHunk: %w", err)
			}
			prevIdx++

			continue
		}

		if prevIdx >= len(prevHunkSegments) {
			// If there aren't anymore previous hunks, then that means any
			// remaining current hunks are new.
			currHunkSegment := currHunkSegments[currIdx]
			if _, err := d.insertStagedLiveHunkSegment(file, commit, currHunkSegment); err != nil {
				return err
			}
			currIdx++

			continue
		}
		currHunkSegment := currHunkSegments[currIdx]
		prevHunkSegment := prevHunkSegments[prevIdx]

		shiftedStart := shiftedStarts[0]
		shiftedEnd := shiftedEnds[0]

		if os.Getenv("CHISEL_DEBUG") == "2" {
			log.Printf("file: %s commit: %s prevIdx: %d shiftedStart: %d shiftedEnd: %d", file, commit, prevIdx, shiftedStart, shiftedEnd)
		}

		info := checkOverlap(shiftedStart, shiftedEnd, prevHunkSegment, currHunkSegment)
		if os.Getenv("CHISEL_DEBUG") == "2" {
			log.Printf("file: %s commit: %s prevIdx: %d currIdx: %d overlapInfo: %v", file, commit, prevIdx, currIdx, info)
		}

		if !info.HasOverlap {
			// If there isn't any overlap, we need to address hunkSegments with
			// the earliest start.
			if shiftedStart < currHunkSegment.Start {
				// Previous Hunk doesn't show up any longer and has retired.
				if err := d.retireLiveHunk(file, prevHunkSegment.Hash, prevHunkSegment.HunkInfo, commit); err != nil {
					return fmt.Errorf("failed to retireLiveHunk: %w", err)
				}

				if err := stepPrev(prevHunkSegment.SegmentType); err != nil {
					return err
				}
			} else {
				// Current hunk is new.
				if _, err := d.insertStagedLiveHunkSegment(file, commit, currHunkSegment); err != nil {
					return err
				}

				currIdx++
			}
			continue
		}

		switch info.OverlapState {
		case secondFurther, firstFurther:
			if info.FullOverlap {
				// This is potentially multiple hunks with a single successor.
				// Prev is fully absorbed and needs to be advanced.
				if err := d.updateLiveHunkEntryAndStage(file, commit, prevHunkSegment, currHunkSegment); err != nil {
					return fmt.Errorf("%w : failed updateLiveHunkEntryAndStage: commit %s, prevHunkSegment: %v", err, commit, prevHunkSegment)
				}
				if err := stepPrev(prevHunkSegment.SegmentType); err != nil {
					return err
				}
			} else {
				// Here we step curr as much as we can.
				// This is one hunk with potentially multiple successors.
				overlap := true

				var successors []*types.HunkSegment
				for overlap {
					successors = append(successors, currHunkSegment)
					currIdx++
					if currIdx >= len(currHunkSegments) {
						break
					}
					currHunkSegment = currHunkSegments[currIdx]
					o := checkOverlap(shiftedStart, shiftedEnd, prevHunkSegment, currHunkSegment)
					if os.Getenv("CHISEL_DEBUG") == "2" {
						log.Printf("file: %s commit: %s prevIdx: %d currIdx: %d overlapInfo: %v", file, commit, prevIdx, currIdx, o)
					}
					overlap = o.HasOverlap
				}

				if err := d.updateLiveHunkEntryAndStage(file, commit, prevHunkSegment, successors...); err != nil {
					return fmt.Errorf("%w : failed updateLiveHunkEntryAndStage: commit %s, prevHunkSegment: %v", err, commit, prevHunkSegment)
				}
				if err := stepPrev(prevHunkSegment.SegmentType); err != nil {
					return err
				}
			}
		case sameEnd:
			prevHash := prevHunkSegment.Hash
			currHash := currHunkSegment.Hash

			if prevHash == currHash {
				// Important:
				// shiftedStart and shiftedEnd aren't 100% accurate.
				// Some shift information can get lost due to aliasing, particularly with
				// blank lines being present in both upstream and downstream edits that conflict
				// with each other. It's not a guarantee that shiftedEnd-shiftedStart is the correct size,
				// but currHunkSegment.Count will be correct.

				if currHunkSegment.Start != prevHunkSegment.Start {
					// This scenario is movement of a live hunk in a file.
					if err := d.updateLiveHunkEntryAndStage(file, commit, prevHunkSegment, currHunkSegment); err != nil {
						return fmt.Errorf("%w : failed updateLiveHunkEntryAndStage: commit %s, prevHunkSegment: %v", err, commit, prevHunkSegment)
					}
				}
			} else {
				// This scenario is a modification.
				if err := d.updateLiveHunkEntryAndStage(file, commit, prevHunkSegment, currHunkSegment); err != nil {
					return fmt.Errorf("%w : failed updateLiveHunkEntryAndStage: commit %s, prevHunkSegment: %v", err, commit, prevHunkSegment)
				}
			}

			if err := stepPrev(prevHunkSegment.SegmentType); err != nil {
				return err
			}
			currIdx++
		}
	}
	return nil
}

func (d *Differ) handleFileDiff(file, commit, mergeBase string) error {
	if err := d.gitDiffAndUpdateHistory(mergeBase, commit, file); err != nil {
		return err
	}

	if os.Getenv("CHISEL_DEBUG") == "2" {
		log.Printf("handleFileDiff: file: %s commit: %s mergeBase: %s", file, commit, mergeBase)
	}

	fDH := d.fileDiffDataHistory[file]
	if fDH.prev == nil {
		err := d.insertAllCurrentHunkSegments(file, commit)
		if err != nil {
			return err
		}
		return nil
	}

	prevFileHistory := fDH.prev
	prevSortedHunkSegments := prevFileHistory.fileDiffHunkSegments.SortedHunkSegments
	prevFromHunkSegments := prevFileHistory.fileDiffHunkSegments.FromHunkSegments
	prevToHunkSegments := prevFileHistory.fileDiffHunkSegments.ToHunkSegments

	currFileHistory := fDH.curr
	currFromHunkSegments := currFileHistory.fileDiffHunkSegments.FromHunkSegments
	currToHunkSegments := currFileHistory.fileDiffHunkSegments.ToHunkSegments

	shiftedBoundaries, err := d.shiftedBoundaries(file, commit, prevSortedHunkSegments)
	if err != nil {
		return err
	}
	toStarts := shiftedBoundaries.toStarts
	toEnds := shiftedBoundaries.toEnds
	fromStarts := shiftedBoundaries.fromStarts
	fromEnds := shiftedBoundaries.fromEnds

	// FromHunkSegments cannot be succeeded by ToHunkSegments and vice
	// versa.  Walk them independently.
	if err = d.handleHunkSegments(file, commit, fromStarts, fromEnds, prevFromHunkSegments, currFromHunkSegments, constants.FromHunkSegment); err != nil {
		return err
	}
	if err = d.handleHunkSegments(file, commit, toStarts, toEnds, prevToHunkSegments, currToHunkSegments, constants.ToHunkSegment); err != nil {
		return err
	}

	return d.commitStagedLiveHunks(file, commit)
}

// filterFiles removes all files not specified in filesMap, if it isn't empty.
func (d *Differ) filterFiles(list []string) []string {
	if len(d.filesMap) == 0 {
		return list
	}

	var filtered []string
	for _, l := range list {
		if d.filesMap[l] {
			filtered = append(filtered, l)
		}
	}

	return filtered
}

// step jumps to the next commit on the first parent chain and performs diff
// analysis between the previous commit on the first parent chain.  If there
// aren't any more commits to analyze, then the call is a NOP.
func (d *Differ) step() error {
	if d.done() {
		return nil
	}
	var err error

	d.position++
	commit := d.uniqueFirstParentCommits[d.position]
	mergeBase := d.mergeBases[d.position]
	// If merge base is empty, then skip.
	// I don't know if this can happen, but being cautious.
	if mergeBase == "" {
		return nil
	}
	numUniqueFirstParentCommits := len(d.uniqueFirstParentCommits)
	if os.Getenv("CHISEL_DEBUG") == "1" {
		startID := d.opts.ProjectConfig.Since.NextStartID()
		log.Printf("On position %d out of %d for project: %s commit: %s orderId: %d", d.position, numUniqueFirstParentCommits-1, d.opts.ProjectConfig.Name, commit, startID+int64(d.position))
	}

	d.residentTime[d.position], err = d.opts.GitClient.CommitTime(commit)
	if err != nil {
		return fmt.Errorf("failed to find CommitTime for commit %s: %v", commit, err)
	}

	diffFilesList, err := d.opts.GitClient.DiffFilesList(mergeBase, commit)
	if err != nil {
		return fmt.Errorf("failed to find DiffFileList between %s and %s: %v", mergeBase, diffFilesList, err)
	}

	diffFilesList = d.filterFiles(diffFilesList)

	if err := d.retireAllHunksConvergedFiles(commit, diffFilesList); err != nil {
		return err
	}

	for _, file := range diffFilesList {
		if err := d.handleFileDiff(file, commit, mergeBase); err != nil {
			return fmt.Errorf("handleFileDiff: file: %s commit: %s mergeBase: %s : %v", file, commit, mergeBase, err)
		}
	}

	return nil
}

// done returns true if all first parent commits have been stepped through.
func (d *Differ) done() bool {
	return d.position >= len(d.uniqueFirstParentCommits)-1
}

func (d *Differ) checkConfig() error {
	if d.opts == nil {
		return errors.New("opts is nil")
	}

	if d.opts.ProjectConfig == nil {
		return errors.New("ProjectConfig is nil")
	}

	if !d.opts.ProjectConfig.ValuesSet() {
		return errors.New("ProjectConfig.ValuesSet() is false")
	}

	return nil
}

func (d *Differ) stepUntilDone(ctx context.Context) (*DiffResults, error) {
	for !d.done() {
		if err := d.step(); err != nil {
			return nil, err
		}
	}
	startID := d.opts.ProjectConfig.Since.NextStartID()
	commits := make([]ContentCommit, len(d.uniqueFirstParentCommits))
	for idx, commit := range d.uniqueFirstParentCommits {
		commits[idx] = ContentCommit{
			ResidentTime: d.residentTime[idx],
			OrderID:      startID + int64(idx),
			CommitBasePair: types.CommitBasePair{
				Commit:    commit,
				MergeBase: d.mergeBases[idx],
			},
		}
	}

	allCommitsConsumed := (d.opts.MaxCommits <= 0) || !d.areCommitsPending
	diffResults := &DiffResults{
		Commits:            commits,
		HashToContent:      d.hashToContent,
		LiveHunkSegments:   d.liveHunkSegments,
		DeadHunkSegments:   d.deadHunkSegments,
		AllCommitsConsumed: allCommitsConsumed,
	}
	return diffResults, nil
}

func (d *Differ) createKeyPrefix() error {
	var buffer bytes.Buffer
	enc := gob.NewEncoder(&buffer)

	log.Printf("Calling RefToSha on ProjectConfig.DownstreamBranch for project %s...", d.projectName())
	downstreamHeadCommit, err := d.opts.GitClient.RefToSha(d.opts.ProjectConfig.DownstreamRef())
	if err != nil {
		return fmt.Errorf("RefToSha(%s) failed for project %s : %w", d.opts.ProjectConfig.DownstreamRef(), d.projectName(), err)
	}

	// ProjectConfig.Since.Commit may be a non-sha reference. Convert it to one
	// when creating key.
	sinceCommit := ""
	if d.opts.ProjectConfig.Since.Commit != constants.BootstrapValue && d.opts.ProjectConfig.Since.Commit != "" {
		log.Printf("Calling RefToSha on ProjectConfig.Since.Commit for project %s...", d.projectName())
		sinceCommit, err = d.opts.GitClient.RefToSha(d.opts.ProjectConfig.Since.Commit)
		if err != nil {
			return fmt.Errorf("RefToSha(%s) failed for project %s : %w", d.opts.ProjectConfig.Since.Commit, d.projectName(), err)
		}
	}

	enc.Encode(struct {
		ProjectConfig *types.ProjectConfig
		HeadCommit    string
		SinceCommit   string
		MaxCommits    int
	}{
		ProjectConfig: d.opts.ProjectConfig,
		HeadCommit:    downstreamHeadCommit,
		SinceCommit:   sinceCommit,
		MaxCommits:    d.opts.MaxCommits,
	})
	d.keyPrefix = strconv.FormatUint(xxhash.Sum64(buffer.Bytes()), 10)

	return nil
}

// Only return new LiveHunks encountered on this run between `Since` and now.
func (d *Differ) reconcileLiveHunkDuplicates() {
	if d.opts.MostRecentLiveHunks == nil {
		return
	}

	for file, m1 := range d.liveHunkSegments {
		mRecentHash, exists := d.opts.MostRecentLiveHunks[file]
		if !exists {
			continue
		}
		mHash := d.liveHunkSegments[file]
		for hash, m2 := range m1 {
			mRecentHunk, exists := mRecentHash[hash]
			if !exists {
				continue
			}
			mHunk := mHash[hash]
			for hunkInfo, uniqueDiff := range m2 {
				foundUniqueDiff, exists := mRecentHunk[hunkInfo]
				if !exists {
					continue
				}
				// Value is already in storage and must be deleted.
				if foundUniqueDiff.ID == uniqueDiff.ID {
					delete(mHunk, hunkInfo)
				}
			}
			if len(mHunk) == 0 {
				delete(mHash, hash)
			}
		}
		if len(mHash) == 0 {
			delete(d.liveHunkSegments, file)
		}
	}
}

// ResurrectHunkListItems mutates an input slice sorted in descending
// order by OrderID to resurrect dead hunks back to life.
// When viewing a set of hunks at a specific point in time, hunks that are
// marked dead may have transitioned to this state at a later point in time.
// In this case, we revive the dead hunk to reflect its state at the point
// in time of interest.
func ResurrectHunkListItems(items []*HunkListItem, orderIDs map[string]int64, cutoffID int64) {
	for _, item := range items {
		if item.IsLive {
			continue
		}

		if orderIDs[item.DeadSha1] > cutoffID {
			resurrectHunkListItem(item)
		}
	}
}

func resurrectHunkListItem(item *HunkListItem) {
	item.DeadState = DeadInfoStateString(DeadStateUnset)
	item.Successors = []int64{}
	item.IsLive = true
	item.DeadSha1 = ""
}

// KeyForProjectDiff provides a key for
// (DownstreamHost, DownstreamBranch, UpstreamHost, UpstreamBranch, Project)
// It cannot be used to differentiate specific SHAs and should not be used for
// scenarios where that is a requirement.
func KeyForProjectDiff(projectConfig *types.ProjectConfig) string {
	var buffer bytes.Buffer
	enc := gob.NewEncoder(&buffer)

	enc.Encode(struct {
		Action           string
		DownstreamHost   string
		DownstreamBranch string
		UpstreamHost     string
		UpstreamBranch   string
		Project          string
	}{
		Action:           DiffResultsAction,
		DownstreamHost:   projectConfig.DownstreamHost,
		DownstreamBranch: projectConfig.DownstreamBranch,
		UpstreamHost:     projectConfig.UpstreamHost,
		UpstreamBranch:   projectConfig.UpstreamBranch,
		Project:          projectConfig.Name,
	})
	return strconv.FormatUint(xxhash.Sum64(buffer.Bytes()), 10)
}

// Checksum returns the checksum of diff results.
// Would have preferred to use encoding/gob, but it isn't deterministic when
// maps are involved.
func Checksum(diffResults *DiffResults) (uint64, error) {
	b, err := json.Marshal(diffResults)
	if err != nil {
		return 0, err
	}

	return xxhash.Sum64(b), nil
}
