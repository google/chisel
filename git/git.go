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

// Package git provides utility functions for interacting with a git repo.
package git

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"chisel/constants"
	"chisel/exec"
	"chisel/hash"
	"chisel/types"
)

var (
	// e.g. 2       0       tests/tests/uirendering/src/android/uirendering/cts/testclasses/ExactCanvasTests.java
	numStatsRegExp = regexp.MustCompile(`^([-\d]+)\s+([-\d]+)\s+(.+)`)

	// e.g.  22 files changed, 574 insertions(+), 104 deletions(-)
	shortStatsRegExp = regexp.MustCompile(`^(\d+) files? changed, ((\d+) insertions?\(\+\), (\d+) deletions?\(-\)|(\d+) insertions?\(\+\)|(\d+) deletions?\(-\))`)

	// e.g. diff --combined Android.bp
	combinedDiffRegExp    = regexp.MustCompile(`^diff --combined (\S+)`)
	mergerInsertionRegExp = regexp.MustCompile(`^\+\+`)
	mergerDeletionRegExp  = regexp.MustCompile(`^\-\-`)

	// e.g. +++ b/Android.bp
	lastLineHeaderRegExp = regexp.MustCompile(`^\+\+\+\s`)

	insertionRegExp = regexp.MustCompile(`^\+(.*)$`)
	deletionRegExp  = regexp.MustCompile(`^\-(.*)$`)
	fileRegexp      = regexp.MustCompile(`^diff --git a/\S+\sb/\S+`)

	renameRegExp          = regexp.MustCompile(`^(\S+)\s=>\s(\S+)`)
	renameRegExpShortHand = regexp.MustCompile(`^(\S*){(\S*)\s=>\s(\S*)}(\S*)`)

	// e.g. @@ -152 +152 @@ public class CameraExtensionSessionTest extends Camera2ParameterizedTestCase {
	hunkHeaderRegExp = regexp.MustCompile(`^@@ [-,+](\d*)(,(\d*))? [+,-](\d*)(,(\d*))? @@`)

	shaBlameLineRegExp = regexp.MustCompile(`^(\S+)`)

	bugRegexp = regexp.MustCompile(`^(?i:bug)\s?:\s?(\w.*)`)
	crsRegexp = regexp.MustCompile(`^(?i:crs-fixed)\s?:\s?(\w.*)`)
)

type parseState int

// Parsing states for pseudo-FSMs
const (
	parseColdStart parseState = iota
	parseNumStatsState
	parseShortStatsState
	parseLastLineHeader
	parseCombinedDiff
	parseDiffs
)

const changeIDLength = 41

// Client provides utility functions for issuing git commands.
type Client struct {
	// Git Repository Path
	Path string

	MaxWorkers int

	Executor CmdExecutor

	FetchRetries int
}

func isDigit(b byte) bool {
	return b >= byte('0') && b <= byte('9')
}

func parseRenameLine(line string) string {
	asBytes := []byte(line)

	if finds := renameRegExpShortHand.FindSubmatch(asBytes); finds != nil {
		srcFile := string(finds[1]) + string(finds[2]) + string(finds[4])
		return filepath.Clean(srcFile)

	}

	if finds := renameRegExp.FindSubmatch(asBytes); finds != nil {
		return string(finds[1])
	}

	return line
}

// Assumptions made: Only a single occurrence of "Change-Id" can exist.
func parseChangeID(body []string) string {
	changeIDMarker := "Change-Id: "
	changeID := ""
	for i := len(body) - 1; i >= 0; i-- {
		line := body[i]
		index := strings.Index(line, changeIDMarker)
		if index == -1 {
			continue
		}

		start := index + len(changeIDMarker)
		end := start + changeIDLength
		// Only capture if the line is sufficiently long enough for a Change-Id.
		if len(line) >= end {
			changeID = line[start:end]
		}
		break
	}
	return changeID
}

// Assumptions:
// The bug messages take the form: "bug: 12345"
// The "bug" portion is case insensitive.  Whitespace may or may not be present
// around the colon.
// The "bug:" portion is first non-whitespace segment of line.
// The IDs can be prepended with URLs, either long or short-form.
// Multiple bugs can be listed on one line separated by a comma or whitespace.
// Multiple bugs can be present in different indices of `body`.
// Best effort is made to match the pattern and errors are not propagated.
func parseBugsGeneric(re *regexp.Regexp, body []string) []int {
	var bugs []int

	addedBugs := make(map[int]bool)
	addBug := func(bug int) {
		if _, ok := addedBugs[bug]; !ok {
			addedBugs[bug] = true
			bugs = append(bugs, bug)
		}
	}

LineLoop:
	for _, line := range body {
		finds := re.FindSubmatch([]byte(line))
		if finds == nil {
			continue
		}

		subLine := string(finds[1])
		start := 0
		seekNextDigit := true
		for idx, c := range subLine {
			if seekNextDigit {
				if isDigit(byte(c)) {
					seekNextDigit = false
					start = idx
				}
				continue
			}

			if byte(c) == byte(',') || byte(c) == byte(' ') || byte(c) == byte('.') {
				bug, err := strconv.Atoi(string(subLine[start:idx]))
				if err != nil {
					continue LineLoop
				}
				seekNextDigit = true
				addBug(bug)
			}
		}
		bug, err := strconv.Atoi(string(subLine[start:]))
		if err != nil {
			continue LineLoop
		}
		addBug(bug)
	}
	return bugs
}

func parseBugs(body []string) []int {
	return parseBugsGeneric(bugRegexp, body)
}

func parseCRsFixed(body []string) []int {
	return parseBugsGeneric(crsRegexp, body)
}

// NumStats lines take the form "<string> <string> <string>" representing
// <insertions> <deletions> <filename>.  Binary files show "-" for insertions
// and deletions.  If a rename occurs, the <filename> portion will either
// look like:
// 1) <original_file> => <new_file>
// 2) /some/path/{<original_file> => <new_file>}
// Note that a rename only shows up with => syntax when the downstream codebase
// does the rename. If the rename occurs upstream, then git diff will show
// diff statistics for both files.
func parseNumStats(finds [][]byte) (*types.NumStatLine, error) {
	// Ignore binary files
	if string(finds[1]) == "-" {
		return nil, nil
	}

	insertions, err := strconv.Atoi(string(finds[1]))
	if err != nil {
		return nil, err
	}
	deletions, err := strconv.Atoi(string(finds[2]))
	if err != nil {
		return nil, err
	}

	file := parseRenameLine(string(finds[3]))

	return &types.NumStatLine{
		File:       file,
		Insertions: insertions,
		Deletions:  deletions,
	}, nil
}

// ShortStats lines take the form "22 files changed, 574 insertions(+), 104 deletions(-)",
// with the 'insertions' and 'deletions' portions only present if applicable.
func parseShortStats(finds [][]byte) (*types.ShortStatLine, error) {
	filesChanged, err := strconv.Atoi(string(finds[1]))
	if err != nil {
		return nil, fmt.Errorf("unable to find filesChanged: %v", err)
	}

	insertions := 0
	deletions := 0
	if finds[3] != nil {
		if insertions, err = strconv.Atoi(string(finds[3])); err != nil {
			return nil, fmt.Errorf("unable to find insertions: %v", err)
		}
		if deletions, err = strconv.Atoi(string(finds[4])); err != nil {
			return nil, fmt.Errorf("unable to find deletions: %v", err)
		}
	} else if finds[5] != nil {
		if insertions, err = strconv.Atoi(string(finds[5])); err != nil {
			return nil, fmt.Errorf("unable to find insertions: %v", err)
		}
	} else {
		if deletions, err = strconv.Atoi(string(finds[6])); err != nil {
			return nil, fmt.Errorf("unable to find deletions: %v", err)
		}
	}

	return &types.ShortStatLine{
		FilesChanged: filesChanged,
		Insertions:   insertions,
		Deletions:    deletions,
	}, nil
}

// New returns a Client with a common default configuration.
func New(path string, maxWorkers int) *Client {
	client := &Client{
		Path:         path,
		MaxWorkers:   maxWorkers,
		Executor:     &exec.Client{},
		FetchRetries: constants.NumFetchRetries,
	}

	return client
}

func findNumStatsOrShortStats(line string) ([][]byte, parseState, error) {
	asBytes := []byte(line)
	if innerFinds := numStatsRegExp.FindSubmatch(asBytes); innerFinds != nil {
		return innerFinds, parseNumStatsState, nil
	}

	if innerFinds := shortStatsRegExp.FindSubmatch(asBytes); innerFinds != nil {
		return innerFinds, parseShortStatsState, nil
	}

	return nil, 0, fmt.Errorf("unable to match line %q", line)
}

func (c *Client) parseNumStatAndShortStat(lines []string) (*types.CommitStats, error) {
	idx := 0
	state := parseColdStart
	numLines := len(lines)
	var finds [][]byte
	var numStats []*types.NumStatLine
	shortStatLine := &types.ShortStatLine{}
	var err error

	// Loop invariant: Except for when state=parseColdStart, `finds` will contain
	// a matched regexp at each iteration start.
	for idx < numLines {
		switch state {
		case parseColdStart:
			if finds, state, err = findNumStatsOrShortStats(lines[idx]); err != nil {
				return nil, err
			}
		case parseNumStatsState:
			numStatLine, err := parseNumStats(finds)
			if err != nil {
				return nil, fmt.Errorf("unable to parse num stats: %v.  Client: %v, line: %s", err, c, lines[idx])
			}
			if numStatLine != nil {
				numStats = append(numStats, numStatLine)
			}

			idx++
			if finds, state, err = findNumStatsOrShortStats(lines[idx]); err != nil {
				return nil, err
			}
		case parseShortStatsState:
			if shortStatLine, err = parseShortStats(finds); err != nil {
				return nil, fmt.Errorf("unable to parseShortStats: %v.  Client: %v, line: %s", err, c, lines[idx])
			}

			idx++
			if idx != numLines {
				return nil, errors.New("shortstat line should be the last line of output")
			}
		}
	}

	return &types.CommitStats{
		NumStats:  numStats,
		ShortStat: shortStatLine,
	}, nil
}

// CommitStats returns CommitStats for a commit using --numstat and --shortstat
// output.
func (c *Client) CommitStats(commit string) (*types.CommitStats, error) {
	cmd := []string{"git", "show", commit, "--numstat", "--shortstat", "-w", "--format="}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return nil, err
	}

	commitStats, err := c.parseNumStatAndShortStat(out)
	if err != nil {
		return nil, fmt.Errorf("commit: %s %v", commit, err)
	}
	return commitStats, nil
}

// CommitDiffStats produces CommitStats between `fromCommit` and `toCommit`.
func (c *Client) CommitDiffStats(fromCommit, toCommit string) (*types.CommitStats, error) {
	cmd := []string{"git", "diff", fromCommit, toCommit, "-w", "--numstat", "--shortstat"}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return nil, err
	}

	commitStats, err := c.parseNumStatAndShortStat(out)
	if err != nil {
		return nil, fmt.Errorf("fromCommit: %s toCommit: %s %v", fromCommit, toCommit, err)
	}
	return commitStats, nil
}

// FirstParentCommits returns all commits reachable from `commit` by walking
// first parent links.  Commits are returned in reverse chronological order.
func (c *Client) FirstParentCommits(ref string) ([]string, error) {
	cmd := []string{"git", "rev-list", ref, "--first-parent"}
	return c.Executor.RunCommandReadStdOut(cmd, c.Path)
}

// UniqueFirstParentCommits returns all commits reachable from `commit` and
// not reachable from anything in `excludeCommits`. Only `commit`'s
// first-parent links are walked. Commits are returned in chronological order.
func (c *Client) UniqueFirstParentCommits(ref string, excludeRefs []string) ([]string, error) {
	cmd := []string{"git", "rev-list", ref, "--first-parent", "--reverse", "--not"}
	cmd = append(cmd, excludeRefs...)
	return c.Executor.RunCommandReadStdOut(cmd, c.Path)
}

// UniqueFirstParentMergeCommits behaves like UniqueFirstParentCommits, but
// returns only merge commits.
func (c *Client) UniqueFirstParentMergeCommits(commit string, excludeCommits []string) ([]string, error) {
	cmd := []string{"git", "rev-list", commit, "--first-parent", "--reverse", "--merges", "--not"}
	cmd = append(cmd, excludeCommits...)
	return c.Executor.RunCommandReadStdOut(cmd, c.Path)
}

// UniqueFirstParentNoMergeCommits behaves like UniqueFirstParentCommits, but
// returns only non-merge commits.
func (c *Client) UniqueFirstParentNoMergeCommits(commit string, excludeCommits []string) ([]string, error) {
	cmd := []string{"git", "rev-list", commit, "--first-parent", "--reverse", "--no-merges", "--not"}
	cmd = append(cmd, excludeCommits...)
	return c.Executor.RunCommandReadStdOut(cmd, c.Path)
}

func (c *Client) parent(commit string, number int) (string, error) {
	caret := commit + "^" + strconv.Itoa(number)
	cmd := []string{"git", "rev-parse", caret}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return "", err
	}

	if len(out) != 1 {
		return "", fmt.Errorf("commit: %s expecting a single line of output, but received %q", commit, out)
	}

	return out[0], nil
}

// FirstParent returns a commit's first parent.
func (c *Client) FirstParent(commit string) (string, error) {
	return c.parent(commit, 1)
}

// SecondParent returns a commit's first parent.
func (c *Client) SecondParent(commit string) (string, error) {
	return c.parent(commit, 2)
}

// IsMergeCommit returns true if a commit is a merge commit and
// false for any other reason.
func (c *Client) IsMergeCommit(commit string) bool {
	_, err := c.SecondParent(commit)

	return err == nil
}

// IsRevertCommit returns true if a commit is a revert commit and
// false for any other reason.  This function relies on partial string
// matching and is susceptible to false positives and negatives.
func (c *Client) IsRevertCommit(commit string) (bool, error) {
	cmd := []string{"git", "show", commit, "--format=%s %b", "-s"}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return false, err
	}

	revertStrings := []string{
		"Reverted Changes:",
		"Reason for revert:",
		"This reverts commit",
	}
	for _, line := range out {
		for _, revertString := range revertStrings {
			if strings.Contains(line, revertString) {
				return true, nil
			}
		}
	}

	return false, nil
}

// CommitType returns the commit type of provided commit.
func (c *Client) CommitType(commitSha1 string) (constants.CommitType, error) {
	if c.IsMergeCommit(commitSha1) {
		return constants.CommitTypeMerge, nil
	}

	ok, err := c.IsRevertCommit(commitSha1)
	if err != nil {
		return constants.CommitTypeUnset, err
	}

	if ok {
		return constants.CommitTypeRevert, nil
	}

	return constants.CommitTypeStandard, nil
}

// CommitTime returns the commit time in unix epoch seconds.
func (c *Client) CommitTime(commit string) (int64, error) {
	cmd := []string{"git", "show", commit, "-s", "--format=%ct"}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return 0, err
	}

	if len(out) != 1 {
		return 0, fmt.Errorf("commit: %s expecting a single line of output, but received %q", commit, out)
	}

	unixTime, err := strconv.ParseInt(out[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to find unixTime: %v.  Client: %v, commit: %s", err, c, commit)
	}

	return unixTime, nil
}

// CommitInfo returns a populated *CommitInfo for the input `commit`.
// Note that Git allows poorly formed strings to be specified for a user email,
// and doesn't do any input validation. This means there is not a reliable
// delimiter and author needs to be parsed last.
func (c *Client) CommitInfo(commit string) (*types.CommitInfo, error) {
	cmd := []string{"git", "show", commit, "-s", `--format=%ct %T %ae`}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return nil, err
	}

	if len(out) != 1 {
		return nil, fmt.Errorf("commit: %s expecting a single line of output, but received %q", commit, out)
	}

	split := strings.SplitN(out[0], " ", 3)
	unixTime, err := strconv.ParseInt(split[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unable to find unixTime: %v.  Client: %v, commit: %s", err, c, commit)
	}
	treeHash := split[1]
	email := ""
	if len(split) > 2 {
		email = split[2]
	}

	// Intentional error ignore:
	// Don't propagate an error on looking up parents.
	// Initial commits won't have parents
	// Non-merge commits won't have second parents
	firstParent, _ := c.FirstParent(commit)
	secondParent, _ := c.SecondParent(commit)

	cmd = []string{"git", "show", commit, "-s", "--format=%s"}
	if out, err = c.Executor.RunCommandReadStdOut(cmd, c.Path); err != nil {
		return nil, err
	}
	subject := strings.Join(out, "\n")

	cmd = []string{"git", "show", commit, "-s", "--format=%b"}
	if out, err = c.Executor.RunCommandReadStdOut(cmd, c.Path); err != nil {
		return nil, err
	}
	body := strings.Join(out, "\n")

	changeID := parseChangeID(out)
	bugs := parseBugs(out)
	crsFixed := parseCRsFixed(out)

	return &types.CommitInfo{
		Email:        email,
		CommitTime:   unixTime,
		TreeHash:     treeHash,
		FirstParent:  firstParent,
		SecondParent: secondParent,
		Subject:      subject,
		Body:         body,
		ChangeID:     changeID,
		Bugs:         bugs,
		CRsFixed:     crsFixed,
	}, nil
}

func commitStatsFromNumStats(numStats []*types.NumStatLine) *types.CommitStats {
	shortStatLine := &types.ShortStatLine{}
	shortStatLine.FilesChanged = len(numStats)

	for _, numStat := range numStats {
		shortStatLine.Insertions += numStat.Insertions
		shortStatLine.Deletions += numStat.Deletions
	}

	return &types.CommitStats{
		NumStats:  numStats,
		ShortStat: shortStatLine,
	}
}

// CommitStatsFromCombinedDiff returns CommitStats for a commit by parsing the
// combined diff output. This should be used for merge commits to more
// accurately track modifications done by the merger. Unique actions taken by
// the merger show up with '++' or '--' in the line.
// The output from --numstat and --shortstat
// are populated based on all the commits that get merged. This adds
// noise to the tracking for a merge commit as well as double counts
// insertions and deletions, as the individual commits are already tracked.
func (c *Client) CommitStatsFromCombinedDiff(commit string) (*types.CommitStats, error) {
	cmd := []string{"git", "show", commit, "-w", "--unified=0", "-c", "--format="}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return nil, err
	}

	state := parseColdStart
	currFile := ""
	insertions := 0
	deletions := 0
	var numStats []*types.NumStatLine
	var finds [][]byte

	updateNumStats := func() {
		if insertions != 0 || deletions != 0 {
			numStats = append(numStats, &types.NumStatLine{
				File:       currFile,
				Insertions: insertions,
				Deletions:  deletions,
			})
		}
	}

	for _, line := range out {
		switch state {
		case parseColdStart:
			if finds = combinedDiffRegExp.FindSubmatch([]byte(line)); finds != nil {
				currFile = string(finds[1])
				state = parseLastLineHeader
			}
		case parseLastLineHeader:
			if lastLineHeaderRegExp.Match([]byte(line)) {
				state = parseCombinedDiff
			}
		case parseCombinedDiff:
			asBytes := []byte(line)
			if finds = combinedDiffRegExp.FindSubmatch(asBytes); finds != nil {
				updateNumStats()
				// Reset for next iterations
				currFile = string(finds[1])
				insertions = 0
				deletions = 0
				state = parseLastLineHeader
			} else if insertMatch := mergerInsertionRegExp.Match(asBytes); insertMatch {
				insertions++
			} else if deleteMatch := mergerDeletionRegExp.Match(asBytes); deleteMatch {
				deletions++
			}

		}
	}
	updateNumStats()

	return commitStatsFromNumStats(numStats), nil
}

// DiffFilesList returns files with differences between `fromCommit` and `toCommit`
func (c *Client) DiffFilesList(fromCommit, toCommit string) ([]string, error) {
	cmd := []string{"git", "diff", fromCommit, toCommit, "-w", "--name-only"}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return nil, err
	}

	return out, nil
}

func extractStartAndCount(finds [][]byte) (*types.HunkHeader, error) {
	var err error
	hl := &types.HunkHeader{
		FromCount: 1,
		ToCount:   1,
	}

	if hl.FromStart, err = strconv.Atoi(string(finds[1])); err != nil {
		return nil, fmt.Errorf("unable to find HunkInfo.FromStart: %v", err)
	}

	if finds[3] != nil {
		if hl.FromCount, err = strconv.Atoi(string(finds[3])); err != nil {
			return nil, fmt.Errorf("unable to find HunkInfo.FromCount: %v", err)
		}
	}

	if hl.ToStart, err = strconv.Atoi(string(finds[4])); err != nil {
		return nil, fmt.Errorf("unable to find HunkInfo.ToStart: %v", err)
	}

	if finds[6] != nil {
		if hl.ToCount, err = strconv.Atoi(string(finds[6])); err != nil {
			return nil, fmt.Errorf("unable to find HunkInfo.ToCount: %v", err)
		}
	}

	return hl, nil
}

func updateFileDiffHunkSegments(fromContent, toContent []string, currHunkHeader *types.HunkHeader, fileDiffHunkSegments *types.FileDiffHunkSegments) {
	var fromHunkSegment *types.HunkSegment
	var toHunkSegment *types.HunkSegment

	if len(fromContent) > 0 {
		fromHunkSegment = &types.HunkSegment{
			HunkInfo: types.HunkInfo{
				Start:       currHunkHeader.FromStart,
				Count:       currHunkHeader.FromCount,
				SegmentType: constants.FromHunkSegment,
			},
			Hash:    hash.OfStringSlice(fromContent),
			Content: fromContent,
		}

		fileDiffHunkSegments.FromHunkSegments = append(fileDiffHunkSegments.FromHunkSegments, fromHunkSegment)
		fileDiffHunkSegments.SortedHunkSegments = append(fileDiffHunkSegments.SortedHunkSegments, fromHunkSegment)
	}

	if len(toContent) > 0 {
		toHunkSegment = &types.HunkSegment{
			HunkInfo: types.HunkInfo{
				Start:       currHunkHeader.ToStart,
				Count:       currHunkHeader.ToCount,
				SegmentType: constants.ToHunkSegment,
			},
			Hash:    hash.OfStringSlice(toContent),
			Content: toContent,
		}

		fileDiffHunkSegments.ToHunkSegments = append(fileDiffHunkSegments.ToHunkSegments, toHunkSegment)
		fileDiffHunkSegments.SortedHunkSegments = append(fileDiffHunkSegments.SortedHunkSegments, toHunkSegment)
	}
}

// FileDiffHunkSegments produces FileDiffHunkSegments between `fromCommit` and `toCommit`
// for `file`.
func (c *Client) FileDiffHunkSegments(fromCommit, toCommit, file string) (*types.FileDiffHunkSegments, error) {
	cmd := []string{"git", "diff", fromCommit, toCommit, "-w", "-U0", "--", file}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return nil, err
	}

	fileDiffHunkSegments := &types.FileDiffHunkSegments{
		Name: file,
	}
	state := parseColdStart
	var (
		finds                  [][]byte
		currHunkHeader         *types.HunkHeader
		fromContent, toContent []string
	)
	secondDiffObserved := false

	for _, line := range out {
		// If a directory is diffed, we need to bail after encountering
		// the next file returned by 'git diff'.
		if secondDiffObserved {
			break
		}

		switch state {
		case parseColdStart:
			if finds = hunkHeaderRegExp.FindSubmatch([]byte(line)); finds != nil {
				if currHunkHeader, err = extractStartAndCount(finds); err != nil {
					return nil, fmt.Errorf("line: %q, Client: %v : %w", line, c, err)
				}
				state = parseDiffs
			}
		case parseDiffs:
			asBytes := []byte(line)

			if finds = hunkHeaderRegExp.FindSubmatch(asBytes); finds != nil {
				nextHunkHeader, err := extractStartAndCount(finds)
				if err != nil {
					return nil, fmt.Errorf("line: %q, Client: %v : %w", line, c, err)
				}

				updateFileDiffHunkSegments(fromContent, toContent, currHunkHeader, fileDiffHunkSegments)

				toContent = nil
				fromContent = nil

				currHunkHeader = nextHunkHeader
			} else if finds = insertionRegExp.FindSubmatch(asBytes); finds != nil {
				toContent = append(toContent, string(finds[1]))
			} else if finds = deletionRegExp.FindSubmatch(asBytes); finds != nil {
				fromContent = append(fromContent, string(finds[1]))
			} else if fileRegexp.Match(asBytes) {
				secondDiffObserved = true
			}
		}
	}
	updateFileDiffHunkSegments(fromContent, toContent, currHunkHeader, fileDiffHunkSegments)

	// It is still possible for the HunkSegments to not be sorted. This ensures that they are.
	sort.Slice(fileDiffHunkSegments.SortedHunkSegments, func(i, j int) bool {
		return fileDiffHunkSegments.SortedHunkSegments[i].Start < fileDiffHunkSegments.SortedHunkSegments[j].Start
	})

	return fileDiffHunkSegments, nil
}

// CommitFromBlame returns the commit responsible for the line at `lineNumber`
// in `file` at `commit`.
func (c *Client) CommitFromBlame(commit, file string, lineNumber int) (string, error) {
	lineArg := "-L" + strconv.Itoa(lineNumber) + "," + strconv.Itoa(lineNumber)
	cmd := []string{"git", "blame", "-w", "-e", "-l", lineArg, commit, "--", file}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return "", err
	}

	if len(out) != 1 {
		return "", errors.New("expecting a single line of output")
	}

	finds := shaBlameLineRegExp.FindSubmatch([]byte(out[0]))
	if finds == nil {
		return "", errors.New("couldn't find SHA")
	}

	return string(finds[1]), nil
}

// LinesInFileAtCommit returns the number of lines of a file at
// a specific commit.
func (c *Client) LinesInFileAtCommit(file, commit string) (int, error) {
	cmd := []string{"git", "show", commit + ":" + file}

	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return 0, err
	}

	return len(out), nil
}

// IsShallow returns true if the repo is shallow, and false for any other
// reason.
func (c *Client) IsShallow() bool {
	if _, err := os.Stat(filepath.Join(c.Path, ".git/shallow")); err != nil {
		return false
	}
	return true
}

// UniqueReachableCommitsFromMergesOptimized finds the unique commits made
// reachable by each merge on `fromCommit`'s first-parent chain that aren't
// reachable from `excludeCommits`.
// A commit may be reachable from multiple 2nd parent commits, but this
// function determines the oldest merge commit and provides a way to classify
// the first time a commit is reachable.
// An example usage model for this would be finding commits brought
// into downstream through merges that aren't present in upstream releases.
// As an example, consider this graph:
/*
// Downstream:    H---I---M1---J---M2
//	          /       /        /
// SomeBranch:  C---D---E----F---G
//	       /
// Release:   A----------------B
*/
// Although D and E are reachable from M2, the earliest merge that makes them
// reachable from M2's first-parent chain is M1.
// The returned map is indexed by CommitSha1 X, and Y's presence in the
// mapped-to slice indicates that Y is uniquely reachable from X.
func (c *Client) UniqueReachableCommitsFromMergesOptimized(ctx context.Context, uniqueFirstParentMergeCommits, excludeCommits []string) (map[string][]string, error) {

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(max(1, min(c.MaxWorkers, len(uniqueFirstParentMergeCommits))))

	var (
		uniqueAncestorCommits   = make(map[string][]string)
		uniqueAncestorCommitsMu sync.Mutex
	)

	for idx := range uniqueFirstParentMergeCommits {

		eg.Go(func() error {
			if err := egCtx.Err(); err != nil {
				return err
			}

			merge := uniqueFirstParentMergeCommits[idx]
			mergeSecondParent := merge + "^2"
			cmd := []string{"git", "rev-list", mergeSecondParent, "--not"}
			// Exclude commits reachable by first parent chain
			cmd = append(cmd, merge+"^1")
			// Exclude anything passed in as an argument
			cmd = append(cmd, excludeCommits...)
			// Exclude all prior merge commits in first parent chain
			cmd = append(cmd, uniqueFirstParentMergeCommits[:idx]...)
			out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
			if err != nil {
				return err
			}

			// Note: I profiled mutex usage vs. channel usage and the
			// mutex version generally performed slightly faster.
			if len(out) > 0 {
				uniqueAncestorCommitsMu.Lock()
				uniqueAncestorCommits[merge] = out
				uniqueAncestorCommitsMu.Unlock()
			}

			return nil
		})
		if egCtx.Err() != nil {
			break
		}
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return uniqueAncestorCommits, nil
}

// UniqueReachableCommitsFromMerges behaves like
// UniqueReachableCommitsFromMergesOptimized, but will find the
// uniqueFirstParentMergeCommits itself.
func (c *Client) UniqueReachableCommitsFromMerges(ctx context.Context, fromCommit string, excludeCommits []string) (map[string][]string, error) {
	uniqueFirstParentMergeCommits, err := c.UniqueFirstParentMergeCommits(fromCommit, excludeCommits)
	if err != nil {
		return nil, err
	}

	return c.UniqueReachableCommitsFromMergesOptimized(ctx, uniqueFirstParentMergeCommits, excludeCommits)
}

// BestMergeBase returns the best merge base between `minTargetRef` and `ref`.
// The best merge base is chosen as the commit that minimizes the delta between
// the merge base and 'minTargetRef'.
func (c *Client) BestMergeBase(minTargetRef, ref string) (string, error) {
	cmd := []string{"git", "merge-base", minTargetRef, ref, "-a"}
	candidates, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return "", err
	}

	if len(candidates) == 0 {
		return "", errors.New("unable to find a merge base")
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}

	best := ""
	minDelta := math.MaxInt32
	for _, candidate := range candidates {
		commitStat, err := c.CommitDiffStats(candidate, minTargetRef)
		if err != nil {
			return "", fmt.Errorf("could not get diff stats between %q and %q", candidate, minTargetRef)
		}
		delta := commitStat.ShortStat.Deletions + commitStat.ShortStat.Insertions
		if delta < minDelta {
			minDelta = delta
			best = candidate
		}
	}

	return best, nil
}

// RefToSha converts a reference to its SHA1.
func (c *Client) RefToSha(ref string) (string, error) {
	cmd := []string{"git", "rev-parse", "--verify", ref}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)

	if err != nil {
		return "", err
	}

	if len(out) == 0 {
		return "", errors.New("unable to find sha")
	}

	return out[0], nil
}

// FetchBranch will fetch the provided branch from the provided remote.
func (c *Client) FetchBranch(remoteName string, branch string) error {
	cmd := []string{"git", "fetch", remoteName, branch}

	_, err := c.Executor.RunCommandReadStdOutWithRetries(cmd, c.Path, c.FetchRetries)
	return err
}

// FetchBranches attempts to fetch from MustFetchTargets and TryFetchTargets.
// Errors are propagated if encountered when fetching MustFetchTargets.
func (c *Client) FetchBranches(MustFetchTargets, TryFetchTargets []string) error {
	for _, branch := range MustFetchTargets {
		split := strings.Split(branch, "/")
		if len(split) == 2 {
			if err := c.FetchBranch(split[0], split[1]); err != nil {
				return fmt.Errorf("unable to fetch %q for %q : %v", branch, c.Path, err)
			}
		}
	}

	for _, branch := range TryFetchTargets {
		split := strings.Split(branch, "/")
		if len(split) == 2 {
			// Intentional error ignore:
			// Allow fetch errors on TryFetchTargets.
			c.FetchBranch(split[0], split[1])
		}
	}

	return nil
}

// InitRepo will initialize a git repo and setup remotes.
func (c *Client) InitRepo(remoteNames, remoteURLs []string) error {
	if len(remoteNames) != len(remoteURLs) {
		return errors.New("mismatched number of remoteNames and remoteURLs")
	}
	if err := os.MkdirAll(c.Path, 0750); err != nil {
		return err
	}
	cmd := []string{"git",
		"init",
	}
	if _, err := c.Executor.RunCommandReadStdOut(cmd, c.Path); err != nil {
		return err
	}

	for idx := range remoteNames {
		remoteName := remoteNames[idx]
		remoteURL := remoteURLs[idx]

		cmd = []string{"git",
			"remote",
			"get-url",
			remoteName,
		}
		// Error ignore is intentional. We'll fall through to the add
		// remote path.
		out, _ := c.Executor.RunCommandReadStdOut(cmd, c.Path)
		currURL := strings.TrimSpace(strings.Join(out, ""))
		// If existing (remoteName, remoteURL) is already set to the intended
		// values, then we can short circuit.
		if remoteURL == currURL {
			continue
		}

		cmd = []string{"git",
			"remote",
			"rm",
			remoteName,
		}
		// Ignore the error here, it can occur when trying to remove a
		// non-existent remote.
		c.Executor.RunCommandReadStdOut(cmd, c.Path)

		cmd = []string{"git",
			"remote",
			"add",
			remoteName,
			remoteURL,
		}
		if _, err := c.Executor.RunCommandReadStdOut(cmd, c.Path); err != nil {
			return err
		}
	}

	return nil
}

// Root returns the top level git directory.
func (c *Client) Root() (string, error) {
	cmd := []string{
		"git",
		"rev-parse",
		"--show-toplevel",
	}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return "", err
	}
	if len(out) != 1 {
		return "", errors.New("expecting a single line of output")
	}

	return out[0], nil
}

// Head returns the head commit
func (c *Client) Head() (string, error) {
	cmd := []string{
		"git",
		"rev-parse",
		"HEAD",
	}
	out, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	if err != nil {
		return "", err
	}
	if len(out) != 1 {
		return "", errors.New("expecting a single line of output")
	}

	return out[0], nil
}

// GarbageCollect runs 'git gc' on the repository
func (c *Client) GarbageCollect() error {
	cmd := []string{
		"git",
		"gc",
	}
	_, err := c.Executor.RunCommandReadStdOut(cmd, c.Path)
	return err
}
