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

package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"log"
	"github.com/gammazero/deque"
	"chisel/constants"
	"chisel/contents"
	"chisel/storage"
	"chisel/types"
)

const (
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorBlue  = "\033[34m"
	colorReset = "\033[0m"
)

func decorateHunkListItem(h *contents.HunkListItem) string {
	if h == nil {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "ID: %d\n", h.ID)
	fmt.Fprintf(&sb, "CommitSha1: %s\n", h.CommitSha1)
	fmt.Fprintf(&sb, "MergeBase: %s\n", h.MergeBase)
	fmt.Fprintf(&sb, "ResidentTime: %s\n", h.ResidentTime)
	fmt.Fprintf(&sb, "ContentHash: %d\n", h.ContentHash)
	fmt.Fprintf(&sb, "IsLive: %v\n", h.IsLive)
	if !h.IsLive {
		fmt.Fprintf(&sb, "DeadState: %s\n", h.DeadState)
		fmt.Fprintf(&sb, "DeadSha1: %s\n", h.DeadSha1)
		if len(h.Successors) > 0 {
			fmt.Fprintf(&sb, "Successors: %v\n", h.Successors)
		}
	}
	fmt.Fprintf(&sb, "%s\n", h.Path)
	prefix := "+"
	color := colorGreen
	if h.SegmentType == constants.HunkSegmentTypeString(constants.FromHunkSegment) {
		prefix = "-"
		color = colorRed
	}
	fmt.Fprintf(&sb, "%s%d,%d\n", prefix, h.Start, h.Count)
	lines := strings.Split(h.Content, "\n")
	sb.WriteString(color)
	for _, l := range lines {
		sb.WriteString(prefix + l + "\n")
	}
	sb.WriteString(colorReset)

	return sb.String()
}

// hunkListItemViews are derived views of the input hunkListItems.
type hunkListItemViews struct {
	// The original list.
	hunkListItems []*contents.HunkListItem

	// Map IDs to their successors.
	successors map[int64][]int64

	// Map IDs to their predecessors.
	predecessors map[int64][]int64

	// The heads of all chains.
	heads []*contents.HunkListItem

	// Map IDs to the associated item.
	id2item map[int64]*contents.HunkListItem

	// Map IDs to the root node of their DAG.
	// Only valid after a call to headForID with intended ID.
	id2head map[int64]*contents.HunkListItem
}

func (h *hunkListItemViews) generate() {
	h.successors = make(map[int64][]int64)
	h.predecessors = make(map[int64][]int64)
	h.id2item = make(map[int64]*contents.HunkListItem)
	h.id2head = make(map[int64]*contents.HunkListItem)

	for _, item := range h.hunkListItems {
		h.id2item[item.ID] = item
		for _, successor := range item.Successors {
			h.successors[item.ID] = append(h.successors[item.ID], successor)
			h.predecessors[successor] = append(h.predecessors[successor], item.ID)

			// In the event that > 1 hunk collapse to a single hunk, meaning
			// multiple items are succeeded by the same item, overwrites of id2head
			// will occur. Functionally, this is OK because eventually the path compression
			// will find the single root node.
			h.id2head[successor] = item
		}
	}

	for _, item := range h.hunkListItems {
		if _, found := h.predecessors[item.ID]; !found {
			h.heads = append(h.heads, item)

			// Head items map to themselves.
			h.id2head[item.ID] = item
		}
	}
}

func (h *hunkListItemViews) headForID(id int64) *contents.HunkListItem {
	item, ok := h.id2head[id]
	if !ok {
		return nil
	}

	if item.ID == id {
		return h.id2head[id]
	}
	head := h.headForID(item.ID)
	// Compress the path.
	h.id2head[id] = head

	return head
}

// dagForLine will search hunkListItems from recent->oldest to find the first
// occurrence of a hunk spanning the desired line, and return the matching DAG.
func (h *hunkListItemViews) dagForLine(line int) []*contents.HunkListItem {
	for _, item := range h.hunkListItems {
		exclusiveEnd := item.Start + item.Count
		if line >= item.Start && line < exclusiveEnd {
			return h.dagForItem(item.ID)
		}
	}

	return nil
}

// Returns the DAG for which 'id' is a member.
func (h *hunkListItemViews) dagForItem(id int64) []*contents.HunkListItem {
	head := h.headForID(id)
	if head == nil {
		return nil
	}

	dag := []*contents.HunkListItem{}

	q := new(deque.Deque[*contents.HunkListItem])
	q.PushBack(head)

	for q.Len() > 0 {
		front := q.PopFront()
		dag = append(dag, front)
		for _, successor := range front.Successors {
			q.PushBack(h.id2item[successor])
		}
	}

	return dag
}

type runContext struct {
	views *hunkListItemViews
	curr  *contents.HunkListItem
}

func help() {
	fmt.Println("S: View Successor")
	fmt.Println("P: View Predecessor")
	fmt.Println("C: Print Current Node")
	fmt.Println("J <ID>: Jump to Node matching ID")
	fmt.Println("HEADS: Print All Chain Heads")
	fmt.Println("Q: Quit")
}

func generateViews(hunkListItems []*contents.HunkListItem) *hunkListItemViews {
	views := &hunkListItemViews{hunkListItems: hunkListItems}
	views.generate()
	return views
}

// DecorateHunkListItems adds ANSI color codes to a string representation of
// a slice of HunkListItems.
func DecorateHunkListItems(items ...*contents.HunkListItem) string {
	var sb strings.Builder
	sb.WriteString("[\n")
	for _, item := range items {
		if item == nil {
			continue
		}
		sb.WriteString("\t{\n")
		lines := strings.SplitSeq(decorateHunkListItem(item), "\n")
		for l := range lines {
			sb.WriteString("\t\t" + l + "\n")
		}
		sb.WriteString("\t},\n")
	}
	sb.WriteString("]\n")

	return sb.String()
}

func printSuccessors(c *runContext) {
	suc, exists := c.views.successors[c.curr.ID]
	if !exists {
		fmt.Println("No successor found")
		return
	}

	for _, s := range suc {
		fmt.Println(DecorateHunkListItems(c.views.id2item[s]))
		c.curr = c.views.id2item[s]
	}

	if len(suc) > 1 {
		fmt.Println("Multiple successors exist.  Jump to desired node.")
	}
}

func printPredecessors(c *runContext) {
	pred, exists := c.views.predecessors[c.curr.ID]
	if !exists {
		fmt.Println("No predecessor found.")
		return
	}

	for _, p := range pred {
		fmt.Println(DecorateHunkListItems(c.views.id2item[p]))
		c.curr = c.views.id2item[p]
	}

	if len(pred) > 1 {
		fmt.Println("Multiple predecessors exist.  Jump to desired node.")
	}
}

// Interact is a simple stdin reader to walk successor chains.
func Interact(ctx context.Context, queryConfig *types.QueryConfig, storage storage.Storage) error {
	if err := checkConfig(queryConfig.ApplicationConfig, constants.UnsetAction); err != nil {
		return err
	}

	if queryConfig.File == "" {
		return errors.New("file is not set")
	}

	if queryConfig.Commit != "" {
		return errors.New("commit should not be specified for interactive mode")
	}

	hunkListItems, err := storage.SortedHunksForFile(ctx, queryConfig)
	if err != nil {
		return err
	}

	if len(hunkListItems) == 0 {
		log.Print("No hunk list items to show")
		return nil
	}

	c := &runContext{}
	c.views = generateViews(hunkListItems)
	c.curr = hunkListItems[0]

	fmt.Println(DecorateHunkListItems(c.views.heads...))

	scanner := bufio.NewScanner(os.Stdin)
	var prevText string
	var text string
	for {
		fmt.Print(colorBlue + "Enter Command:" + colorReset)

		if success := scanner.Scan(); !success {
			return nil
		}
		text = strings.ToLower(scanner.Text())
		if text == "" {
			text = prevText
		}
		prevText = text

		var inputID int64
		n, err := fmt.Sscanf(text, "j %d", &inputID)

		if err == nil {
			if n == 1 {
				fmt.Println(DecorateHunkListItems(c.views.id2item[inputID]))
				c.curr = c.views.id2item[inputID]

				continue
			}
		}

		if text == "s" {
			printSuccessors(c)
		} else if text == "p" {
			printPredecessors(c)
		} else if text == "c" {
			fmt.Println(DecorateHunkListItems(c.curr))
		} else if text == "h" {
			help()
		} else if text == "heads" {
			fmt.Println(DecorateHunkListItems(c.views.heads...))
		} else if text == "q" {
			break
		} else {
			fmt.Println("Unknown command.")
			help()
		}
	}

	return nil
}
