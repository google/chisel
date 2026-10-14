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

package repositories

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"chisel/stats"
)

type commitStatsGroup struct {
	commit *stats.Commit
	files  []*stats.File

	branchCommit *stats.BranchCommit
	branchFiles  []*stats.BranchFile
}

type commitStatsChunkGroup struct {
	*commitStatsGroup
	state chunkState
}

type chunkStatsGroup struct {
	commits []*stats.Commit
	files   [][]*stats.File

	branchCommits []*stats.BranchCommit
	branchFiles   [][]*stats.BranchFile

	state chunkState
}

func (c chunkStatsGroup) String() string {
	sb := new(strings.Builder)

	newLine := false
	for idx, commit := range c.commits {
		fmt.Fprintf(sb, "commits[%d]: %v ", idx, commit)
		newLine = true
	}
	if newLine {
		sb.WriteString("\n")
	}

	newLine = false
	for idx, innerFiles := range c.files {
		for idy, file := range innerFiles {
			fmt.Fprintf(sb, "file[%d][%d]: %v ", idx, idy, file)
			newLine = true
		}
	}
	if newLine {
		sb.WriteString("\n")
	}

	newLine = false
	for idx, branchCommit := range c.branchCommits {
		fmt.Fprintf(sb, "branchCommits[%d]: %v ", idx, branchCommit)
		newLine = true
	}
	if newLine {
		sb.WriteString("\n")
	}

	newLine = false
	for idx, innerFiles := range c.branchFiles {
		for idy, file := range innerFiles {
			fmt.Fprintf(sb, "branchFile[%d][%d]: %v ", idx, idy, file)
			newLine = true
		}
	}
	if newLine {
		sb.WriteString("\n")
	}
	fmt.Fprintf(sb, "state: %v\n", chunkStateString(c.state))

	return sb.String()
}

func groupByCommit(statObjects *stats.StatObjects) map[string]*commitStatsGroup {
	if statObjects == nil {
		return nil
	}

	mapped := make(map[string]*commitStatsGroup)

	if statObjects.CommitObjects != nil {
		for sha := range statObjects.CommitObjects.BranchCommits {
			mapped[sha] = &commitStatsGroup{}
		}

		for sha, commitObj := range statObjects.CommitObjects.Commits {
			mapped[sha].commit = commitObj
		}
		for sha, branchCommitObj := range statObjects.CommitObjects.BranchCommits {
			mapped[sha].branchCommit = branchCommitObj
		}
	}

	if statObjects.FileObjects != nil {
		for sha, fileObjs := range statObjects.FileObjects.Files {
			if fileObjs != nil {
				mapped[sha].files = *fileObjs
			}
		}

		for sha, branchFileObjs := range statObjects.FileObjects.BranchFiles {
			if branchFileObjs != nil {
				mapped[sha].branchFiles = *branchFileObjs
			}
		}
	}

	return mapped
}

// statsChunker breaks up StatObjects into groups of rows that don't exceed the
// configured number of maxRows.
// As many Commits, along with their stat data, are grouped together as possible.
// If the stats for all commits are present in the group, then the chunk group
// is `chunkStateFull`, indicating that the entire group can be
// written as an atomic unit.
// In the event that a single commit cannot entirely be contained in a single
// chunk group, the group is `chunkStatePartial*`, indicating that multiple
// next() calls are required.
type statsChunker struct {
	maxRows      int
	statObjects  *stats.StatObjects
	dataByCommit map[string]*commitStatsGroup
	commits      []string

	commitIdx int

	// Per commit chunking cursors
	fileIdx        int
	branchFileIdx  int
	sampledCommits bool
}

func newStatsChunker(maxRows int, statObjects *stats.StatObjects) *statsChunker {
	c := &statsChunker{
		maxRows:     maxRows,
		statObjects: statObjects,
	}
	c.groupByCommit()

	return c
}

func (c *statsChunker) resetCommitCursors() {
	c.fileIdx = 0
	c.branchFileIdx = 0
	c.sampledCommits = false
}

func (c *statsChunker) incrementCommitIdx() {
	c.commitIdx++
	c.resetCommitCursors()
}

func (c *statsChunker) groupByCommit() {
	c.dataByCommit = groupByCommit(c.statObjects)
	c.commits = slices.Collect(maps.Keys(c.dataByCommit))

	// Ensure that commits are sorted by OrderId.
	if len(c.commits) > 0 {
		slices.SortFunc(c.commits, func(a, b string) int {
			bcA := c.dataByCommit[a].branchCommit
			bcB := c.dataByCommit[b].branchCommit
			if bcA == nil || bcB == nil {
				return 0
			}
			if bcA.OrderID < bcB.OrderID {
				return -1
			}
			if bcA.OrderID > bcB.OrderID {
				return 1
			}
			return 0
		})
	}
}

func (c *statsChunker) nextChunkGroupForCommit(commit string, maxRows int) commitStatsChunkGroup {
	// When initially configured as unbounded, always return everything.
	if c.maxRows <= 0 {
		return commitStatsChunkGroup{
			commitStatsGroup: c.dataByCommit[commit],
			state:            chunkStateFull,
		}
	}

	// Ensure we can take at least two rows so that the commit and branchCommit
	// can go together.
	if maxRows < 2 {
		return commitStatsChunkGroup{state: chunkStatePartialPending}
	}

	var (
		statsCommit  *stats.Commit
		branchCommit *stats.BranchCommit
	)
	files := c.dataByCommit[commit].files
	branchFiles := c.dataByCommit[commit].branchFiles

	remainingRows := maxRows
	// We will only submit the commit and branchCommit on the first
	// grouping of a commit. They need to exist first to satisfy SQL REFERENCES clauses.
	if !c.sampledCommits {
		if remainingRows > 0 {
			statsCommit = c.dataByCommit[commit].commit
			if statsCommit != nil {
				remainingRows--
			}
		}

		if remainingRows > 0 {
			branchCommit = c.dataByCommit[commit].branchCommit
			if branchCommit != nil {
				remainingRows--
			}
		}
	}
	c.sampledCommits = true

	fileGroup := nextKeysFromPos(c.fileIdx, remainingRows, files)
	// This is intentional - an index exceeding the size of the array means the next
	// grouping attempt will grab nothing for this array
	c.fileIdx += remainingRows
	remainingRows -= len(fileGroup)

	branchFileGroup := nextKeysFromPos(c.branchFileIdx, remainingRows, branchFiles)
	c.branchFileIdx += remainingRows
	remainingRows -= len(branchFileGroup)

	full := len(fileGroup) == len(files) && len(branchFileGroup) == len(branchFiles)
	var state chunkState

	if full {
		state = chunkStateFull
	} else {
		pending := c.fileIdx < len(files) || c.branchFileIdx < len(branchFiles)

		if pending {
			state = chunkStatePartialPending
		} else {
			state = chunkStatePartialEnd
		}
	}

	return commitStatsChunkGroup{
		commitStatsGroup: &commitStatsGroup{
			commit:       statsCommit,
			branchCommit: branchCommit,
			files:        fileGroup,
			branchFiles:  branchFileGroup,
		},
		state: state,
	}
}

func (c *statsChunker) all() (chunkStatsGroup, error) {
	var (
		commits       []*stats.Commit
		files         [][]*stats.File
		branchCommits []*stats.BranchCommit
		branchFiles   [][]*stats.BranchFile
	)

	for sha, group := range c.dataByCommit {
		if group.commit == nil && len(group.files) > 0 {
			return chunkStatsGroup{}, fmt.Errorf("commit in the group is nil but files is populated for sha: %q", sha)
		}
		if group.branchCommit == nil && len(group.branchFiles) > 0 {
			return chunkStatsGroup{}, fmt.Errorf("branch commit in the group is nil but branch files is populated for sha: %q", sha)
		}

		if group.commit != nil {
			commits = append(commits, group.commit)
			files = append(files, group.files)
		}
		if group.branchCommit != nil {
			branchCommits = append(branchCommits, group.branchCommit)
			branchFiles = append(branchFiles, group.branchFiles)
		}
	}

	// Move commitIdx to the end
	c.commitIdx = len(c.statObjects.CommitObjects.BranchCommits)

	return chunkStatsGroup{
		commits:       commits,
		files:         files,
		branchCommits: branchCommits,
		branchFiles:   branchFiles,
		state:         chunkStateFull,
	}, nil
}

// Done returns true if all commits have been consumed with calls to next().
func (c *statsChunker) Done() bool {
	if c.statObjects == nil {
		return true
	}

	if c.statObjects.CommitObjects == nil {
		return true
	}

	return c.commitIdx >= len(c.statObjects.CommitObjects.BranchCommits)
}

func (c *statsChunker) next() (chunkStatsGroup, error) {

	if c.Done() {
		return chunkStatsGroup{state: chunkStateEnd}, nil
	}

	if c.maxRows <= 0 {
		return c.all()
	}

	if c.maxRows < 2 {
		return chunkStatsGroup{state: chunkStateEnd}, errors.New("maxRows must be at least 2")
	}

	cg := chunkStatsGroup{}
	remainingRows := c.maxRows
	for {
		if c.Done() {
			cg.state = chunkStateFull
			return cg, nil
		}

		commit := c.commits[c.commitIdx]
		commitGroup := c.nextChunkGroupForCommit(commit, remainingRows)

		switch commitGroup.state {
		case chunkStateFull:
			cg.commits = append(cg.commits, commitGroup.commit)
			cg.files = append(cg.files, commitGroup.files)
			cg.branchCommits = append(cg.branchCommits, commitGroup.branchCommit)
			cg.branchFiles = append(cg.branchFiles, commitGroup.branchFiles)
			c.incrementCommitIdx()
			if commitGroup.commit != nil {
				remainingRows--
			}
			if commitGroup.branchCommit != nil {
				remainingRows--
			}
			remainingRows -= len(commitGroup.files) + len(commitGroup.branchFiles)
		case chunkStatePartialPending, chunkStatePartialEnd:
			// We could not consume this commit entirely.
			// Two scenarios here:
			// 1) We do have some commits that can be chunked.
			// We can return those and then reset indices, so that we can
			// attempt to atomically grab this commit on the following next() call.
			if len(cg.commits) > 0 {
				cg.state = chunkStateFull
				c.resetCommitCursors()
				return cg, nil
			}

			// 2) We cannot atomically write a single commit. We need to do this one
			// in chunks.
			cg.state = commitGroup.state
			cg.commits = append(cg.commits, commitGroup.commit)
			cg.files = append(cg.files, commitGroup.files)
			cg.branchCommits = append(cg.branchCommits, commitGroup.branchCommit)
			cg.branchFiles = append(cg.branchFiles, commitGroup.branchFiles)
			if commitGroup.state == chunkStatePartialEnd {
				c.incrementCommitIdx()
			}

			lenCommits := len(cg.commits)
			lenFiles := len(cg.files)
			lenBranchCommits := len(cg.branchCommits)
			lenBranchFiles := len(cg.branchFiles)
			// We can have only a single commit here
			isConsistent := (lenCommits <= 1 && lenFiles == lenCommits) &&
				(lenBranchCommits <= 1 && lenBranchCommits == lenBranchFiles)
			if !isConsistent {
				return cg, fmt.Errorf("chunkGroup is not consistent. lenCommits: %d lenFiles: %d lenBranchCommits: %d lenBranchFiles: %d", lenCommits, lenFiles, lenBranchCommits, lenBranchFiles)
			}

			return cg, nil
		}
	}
}
