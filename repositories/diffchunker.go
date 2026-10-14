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

	"chisel/contents"
)

// compositeLiveKey forms a composite from live hunk segment keys.
type compositeLiveKey struct {
	file     string
	hash     int64
	hunkInfo uint64
}

// compositeDeadKey forms a composite from dead hunk segment keys.
type compositeDeadKey struct {
	commit string
	compositeLiveKey
}

// chunkState holds pseudo-enumerated constants for chunk states.
type chunkState int

// The enumerated values for chunkState.
const (
	chunkStateUnset chunkState = iota

	// The commit(s) present in the chunk are fully represented.
	chunkStateFull

	// The commit present in the chunk is partially represented, and there
	// are pending hunks to be chunked.
	chunkStatePartialPending

	// The commit present in the chunk is partially represented, and
	// this is the last chunk.
	chunkStatePartialEnd

	// All chunks have been consumed.
	chunkStateEnd
)

// chunkStateString emulates a constant map of int to string.
func chunkStateString(state chunkState) string {
	switch state {
	case chunkStateUnset:
		return "Unset"
	case chunkStateFull:
		return "Full"
	case chunkStatePartialPending:
		return "PartialPending"
	case chunkStatePartialEnd:
		return "PartialEnd"
	default:
		return ""
	}
}

func groupKeysByCommit(liveHunkSegments contents.LiveHunkSegmentsData, deadHunkSegments contents.DeadHunkSegmentsData) (map[string][]compositeLiveKey, map[string][]compositeDeadKey) {
	liveKeysByCommit := make(map[string][]compositeLiveKey)
	deadKeysByCommit := make(map[string][]compositeDeadKey)

	for file, m1 := range liveHunkSegments {
		for hash, m2 := range m1 {
			for h, diffID := range m2 {
				liveKeysByCommit[diffID.CommitSha1] = append(liveKeysByCommit[diffID.CommitSha1], compositeLiveKey{file, hash, h})
			}
		}
	}

	for file, m1 := range deadHunkSegments {
		for hash, m2 := range m1 {
			for h, m3 := range m2 {
				for commit, m4 := range m3 {
					deadKeysByCommit[m4.DeadSha1] = append(deadKeysByCommit[m4.DeadSha1],
						compositeDeadKey{commit: commit, compositeLiveKey: compositeLiveKey{file, hash, h}})
				}
			}
		}
	}

	return liveKeysByCommit, deadKeysByCommit
}

type commitDiffChunkGroup struct {
	liveKeys []compositeLiveKey
	deadKeys []compositeDeadKey
	state    chunkState
}

type chunkDiffGroup struct {
	commits  []contents.ContentCommit
	liveKeys [][]compositeLiveKey
	deadKeys [][]compositeDeadKey
	state    chunkState
}

// diffChunker breaks up DiffResults into groups of rows that don't exceed the
// configured number of maxRows.
// As many Commits, along with their hunk data, are grouped together as possible.
// If the hunks for all commits are present in the group, then the chunk group
// is `chunkStateFull`, indicating that the entire group can be
// written as an atomic unit.
// In the event that a single commit cannot entirely be contained in a single
// chunk group, the group is `chunkStatePartial*`, indicating that multiple
// next() calls are required.
type diffChunker struct {
	maxRows          int
	diffResults      *contents.DiffResults
	liveKeysByCommit map[string][]compositeLiveKey
	deadKeysByCommit map[string][]compositeDeadKey

	commitIdx int
	liveIdx   int
	deadIdx   int
}

func newDiffChunker(maxRows int, diffResults *contents.DiffResults) *diffChunker {
	c := &diffChunker{
		maxRows:     maxRows,
		diffResults: diffResults,
	}
	c.groupByCommit()

	return c
}

func (c *diffChunker) incrementCommitIdx() {
	c.commitIdx++
	c.liveIdx = 0
	c.deadIdx = 0
}

func (c *diffChunker) groupByCommit() {
	c.liveKeysByCommit, c.deadKeysByCommit = groupKeysByCommit(c.diffResults.LiveHunkSegments, c.diffResults.DeadHunkSegments)
}

func nextKeysFromPos[T []E, E any](idx, maxKeys int, keys T) T {
	if maxKeys <= 0 {
		return nil
	}

	if idx >= len(keys) {
		return nil
	}

	end := min(idx+maxKeys, len(keys))
	return keys[idx:end]
}

func (c *diffChunker) nextKeysForCommit(commit string, maxRows int) commitDiffChunkGroup {
	liveKeys := c.liveKeysByCommit[commit]
	deadKeys := c.deadKeysByCommit[commit]

	// When initially configured as unbounded, always return everything.
	if c.maxRows <= 0 {
		return commitDiffChunkGroup{
			liveKeys: liveKeys,
			deadKeys: deadKeys,
			state:    chunkStateFull,
		}
	}

	remainingRows := maxRows
	liveGroup := nextKeysFromPos(c.liveIdx, remainingRows, liveKeys)
	c.liveIdx += remainingRows
	remainingRows -= len(liveGroup)

	deadGroup := nextKeysFromPos(c.deadIdx, remainingRows, deadKeys)
	c.deadIdx += remainingRows
	remainingRows -= len(deadGroup)

	full := len(liveGroup) == len(liveKeys) && len(deadGroup) == len(deadKeys)
	var state chunkState

	if full {
		state = chunkStateFull
	} else {
		pending := c.liveIdx < len(liveKeys) || c.deadIdx < len(deadKeys)

		if pending {
			state = chunkStatePartialPending
		} else {
			state = chunkStatePartialEnd
		}
	}

	return commitDiffChunkGroup{
		liveKeys: liveGroup,
		deadKeys: deadGroup,
		state:    state,
	}
}

func (c *diffChunker) all() chunkDiffGroup {
	var (
		liveKeys [][]compositeLiveKey
		deadKeys [][]compositeDeadKey
	)

	for _, commit := range c.diffResults.Commits {
		liveKeys = append(liveKeys, c.liveKeysByCommit[commit.Commit])
		deadKeys = append(deadKeys, c.deadKeysByCommit[commit.Commit])
	}

	c.commitIdx = len(c.diffResults.Commits)

	return chunkDiffGroup{
		commits:  c.diffResults.Commits,
		liveKeys: liveKeys,
		deadKeys: deadKeys,
		state:    chunkStateFull,
	}
}

// Done returns true if all commits have been consumed with calls to next().
func (c *diffChunker) Done() bool {
	return c.commitIdx >= len(c.diffResults.Commits)
}

func (c *diffChunker) next() (chunkDiffGroup, error) {

	if c.Done() {
		return chunkDiffGroup{state: chunkStateEnd}, nil
	}

	if c.maxRows <= 0 {
		return c.all(), nil
	}

	cg := chunkDiffGroup{}
	remainingRows := c.maxRows
	for {
		if c.Done() {
			cg.state = chunkStateFull
			return cg, nil
		}

		contentCommit := c.diffResults.Commits[c.commitIdx]
		commitGroup := c.nextKeysForCommit(contentCommit.Commit, remainingRows)

		switch commitGroup.state {
		case chunkStateFull:
			cg.commits = append(cg.commits, contentCommit)
			cg.liveKeys = append(cg.liveKeys, commitGroup.liveKeys)
			cg.deadKeys = append(cg.deadKeys, commitGroup.deadKeys)
			c.incrementCommitIdx()
			remainingRows -= len(commitGroup.liveKeys) + len(commitGroup.deadKeys)
		case chunkStatePartialPending, chunkStatePartialEnd:
			// We could not consume this commit entirely.
			// Two scenarios here:
			// 1) We do have some commits that can be chunked.
			// We can return those and then reset indices, so that we can
			// attempt to atomically grab this commit on the following next() call.
			if len(cg.commits) > 0 {
				cg.state = chunkStateFull
				c.liveIdx = 0
				c.deadIdx = 0
				return cg, nil
			}

			// 2) We cannot atomically write a single commit. We need to do this one
			// in chunks.
			cg.state = commitGroup.state
			cg.commits = append(cg.commits, contentCommit)
			cg.liveKeys = append(cg.liveKeys, commitGroup.liveKeys)
			cg.deadKeys = append(cg.deadKeys, commitGroup.deadKeys)
			if commitGroup.state == chunkStatePartialEnd {
				c.incrementCommitIdx()
			}
			isSingleCommit := len(cg.commits) == 1 && len(cg.liveKeys) == 1 && len(cg.deadKeys) == 1
			if !isSingleCommit {
				return cg, errors.New("chunkGroup contains more than 1 commit")
			}

			return cg, nil
		}
	}
}
