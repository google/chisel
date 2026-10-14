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

// Package datasinks receives data from the use case handlers and deals with
// presentation logic.
package datasinks

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"chisel/constants"
	"chisel/contents"
	"chisel/handlers"
	"chisel/types"
)

const (
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorBlue  = "\033[34m"
	colorReset = "\033[0m"
)

const (
	defaultPadding = 15
)

// Formatter will write a formatted diff to the provided writer.
type Formatter interface {
	Format(left, right []string, leftStart, rightStart int, writer io.Writer) error
}

// BasicWriter implements the DataSinker interface and adds some color decoration
// to data. It doesn't use any diff formatting for hunks.
type BasicWriter struct {
	Writer io.Writer
}

// Commit is a datasink for the `list commit` use case.
func (d *BasicWriter) Commit(commit *contents.CommitListItem) error {
	if d.Writer == nil {
		return errors.New("nil Writer")
	}

	return handlers.WriteAsJSON(d.Writer, commit)
}

// Commits is a datasink for the `list commits` use case.
func (d *BasicWriter) Commits(commits []*contents.CommitListItem) error {
	if d.Writer == nil {
		return errors.New("nil Writer")
	}

	return handlers.WriteAsJSON(d.Writer, commits)
}

// Files is a datasink for the `list files` use case.
func (d *BasicWriter) Files(files []string) error {
	if d.Writer == nil {
		return errors.New("nil Writer")
	}

	return handlers.WriteAsJSON(d.Writer, files)
}

// Hunk is a datasink for the `list hunk` use case.
func (d *BasicWriter) Hunk(hunk *contents.HunkListItem) error {
	if d.Writer == nil {
		return errors.New("nil Writer")
	}

	decorated := handlers.DecorateHunkListItems(hunk)
	_, err := d.Writer.Write([]byte(decorated))
	return err
}

// Hunks is a datasink for the `list hunks` use case.
func (d *BasicWriter) Hunks(hunks []*contents.HunkListItem) error {
	if d.Writer == nil {
		return errors.New("nil Writer")
	}

	decorated := handlers.DecorateHunkListItems(hunks...)
	_, err := d.Writer.Write([]byte(decorated))
	return err
}

// DAG is a datasink for the `list dag` use case.
func (d *BasicWriter) DAG(hunks []*contents.HunkListItem) error {
	return d.Hunks(hunks)
}

func flatCommit(commit *contents.CommitListItem, writer io.Writer) error {
	if writer == nil {
		return errors.New("nil Writer")
	}
	if commit == nil {
		return nil
	}

	var bytes bytes.Buffer
	fmt.Fprintf(&bytes, "%s %s\n", pad("Commit:"), commit.CommitSha1)
	fmt.Fprintf(&bytes, "%s %s\n", pad("MergeBase:"), commit.MergeBase)
	fmt.Fprintf(&bytes, "%s %s\n", pad("ResidentTime:"), commit.ResidentTime)
	fmt.Fprintf(&bytes, "%s %d\n", pad("OrderID:"), commit.OrderID)

	_, err := writer.Write(bytes.Bytes())

	return err
}

func flatCommits(commits []*contents.CommitListItem, writer io.Writer) error {
	if writer == nil {
		return errors.New("nil Writer")
	}

	for i, commit := range commits {
		if err := flatCommit(commit, writer); err != nil {
			return err
		}

		if i < len(commits)-1 {
			if _, err := writer.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}

	return nil
}

func flatFiles(files []string, writer io.Writer) error {
	if writer == nil {
		return errors.New("nil Writer")
	}

	var bytes bytes.Buffer
	for _, f := range files {
		bytes.WriteString(f + "\n")
	}

	_, err := writer.Write(bytes.Bytes())

	return err
}

type contentAndStart struct {
	content []string
	start   int
}

func resolveLeftAndRight(prev, curr *contents.HunkListItem) (contentAndStart, contentAndStart) {
	transform := func(h *contents.HunkListItem) contentAndStart {
		if h == nil {
			return contentAndStart{}
		}

		return contentAndStart{
			content: strings.Split(h.Content, "\n"),
			start:   h.Start,
		}
	}

	prevContentAndStart := transform(prev)
	currContentAndStart := transform(curr)

	// The common case is where prev is defined. Here, left is prev and right
	// is current.
	if prev != nil {
		return prevContentAndStart, currContentAndStart
	}

	// If only curr is defined, left and right are dependent on segment type.
	// When type is To, we want to show this as a value add, otherwise show as
	// a deletion.
	if curr.SegmentType == constants.HunkSegmentTypeString(constants.ToHunkSegment) {
		return prevContentAndStart, currContentAndStart
	}

	return currContentAndStart, prevContentAndStart
}

func flatHunk(prev, curr *contents.HunkListItem, formatter Formatter, writer io.Writer) error {
	if formatter == nil {
		return errors.New("nil Formatter")
	}
	if writer == nil {
		return errors.New("nil Writer")
	}
	if curr == nil {
		return nil
	}

	var bytes bytes.Buffer
	fmt.Fprintf(&bytes, "%s %d\n", pad("ID:"), curr.ID)
	fmt.Fprintf(&bytes, "%s %d\n", pad("ContentHash:"), curr.ContentHash)
	fmt.Fprintf(&bytes, "%s %s\n", pad("Commit:"), curr.CommitSha1)
	fmt.Fprintf(&bytes, "%s %s\n", pad("MergeBase:"), curr.MergeBase)
	fmt.Fprintf(&bytes, "%s %s\n", pad("ResidentTime:"), curr.ResidentTime)
	fmt.Fprintf(&bytes, "%s %s\n", pad("SegmentType:"), curr.SegmentType)
	if curr.IsLive {
		fmt.Fprintf(&bytes, "%s %s\n", pad("State:"), "Live")
	} else {
		fmt.Fprintf(&bytes, "%s %s\n", pad("State:"), curr.DeadState)
		fmt.Fprintf(&bytes, "%s %s\n", pad("DeadSha1:"), curr.DeadSha1)
		if len(curr.Successors) > 0 {
			fmt.Fprintf(&bytes, "%s %v\n", pad("Successors:"), curr.Successors)
		}
	}
	fmt.Fprintf(&bytes, "%s\n", curr.Path)

	left, right := resolveLeftAndRight(prev, curr)
	if err := formatter.Format(left.content, right.content, left.start, right.start, &bytes); err != nil {
		return err
	}

	_, err := writer.Write(bytes.Bytes())

	return err
}

// FlatWriter implements the DataSinker interface and flattens the output
// similar to Git. JSON-esque delimiters are limited. It also uses a diff
// formatter.
type FlatWriter struct {
	Writer    io.Writer
	Formatter Formatter
}

// Commit is a datasink for the `list commit` use case.
func (f *FlatWriter) Commit(commit *contents.CommitListItem) error {
	return flatCommit(commit, f.Writer)
}

// Commits is a datasink for the `list commits` use case.
func (f *FlatWriter) Commits(commits []*contents.CommitListItem) error {
	return flatCommits(commits, f.Writer)
}

// Files is a datasink for the `list files` use case.
func (f *FlatWriter) Files(files []string) error {
	return flatFiles(files, f.Writer)
}

// Hunk is a datasink for the `list hunk` use case.
func (f *FlatWriter) Hunk(hunk *contents.HunkListItem) error {
	return flatHunk(nil, hunk, f.Formatter, f.Writer)
}

// Hunks is a datasink for the `list hunks` use case.
func (f *FlatWriter) Hunks(hunks []*contents.HunkListItem) error {
	if f.Writer == nil {
		return errors.New("nil Writer")
	}

	for i, hunk := range hunks {
		if err := f.Hunk(hunk); err != nil {
			return err
		}

		if i < len(hunks)-1 {
			if _, err := f.Writer.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}

	return nil

}

// DAG is a datasink for the `list dag` use case.
func (f *FlatWriter) DAG(hunks []*contents.HunkListItem) error {
	var prev *contents.HunkListItem
	for i, hunk := range hunks {
		if err := flatHunk(prev, hunk, f.Formatter, f.Writer); err != nil {
			return err
		}
		prev = hunk

		if i < len(hunks)-1 {
			if _, err := f.Writer.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}

	return nil
}

// LatestCommit is a datasink for the `list latest_*_commit` use case.
func (f *FlatWriter) LatestCommit(commit string) error {
	_, err := f.Writer.Write([]byte(commit))
	return err
}

// LatestCommits is a datasink for the `list latest_*_commits` use case.
func (f *FlatWriter) LatestCommits(commits []types.ProjectCommit) error {
	for _, commit := range commits {
		var bytes bytes.Buffer
		fmt.Fprintf(&bytes, "%s %s\n", pad("Project:"), commit.Project)
		fmt.Fprintf(&bytes, "%s %s\n", pad("Commit:"), commit.Commit)
		bytes.WriteString("\n")
		_, err := f.Writer.Write(bytes.Bytes())
		if err != nil {
			return err
		}
	}

	return nil
}

func padAmount(s string, amount int) string {
	return fmt.Sprintf("%-*s", amount, s)
}

func pad(s string) string {
	return padAmount(s, defaultPadding)
}
