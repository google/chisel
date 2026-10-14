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
	"context"

	"chisel/contents"
	"chisel/types"
)

func resurrectHunkListItems(ctx context.Context, s commitsStore, queryConfig *types.QueryConfig, sortedHunks []*contents.HunkListItem, maxID int64) error {
	commitConfig := *queryConfig
	commitConfig.Commit = ""
	commitConfig.BeforeCommit = ""
	sortedCommits, err := s.SortedCommits(ctx, &commitConfig)
	if err != nil {
		return err
	}
	orderIDs := make(map[string]int64)
	for _, c := range sortedCommits {
		orderIDs[c.CommitSha1] = c.OrderID
	}

	contents.ResurrectHunkListItems(sortedHunks, orderIDs, maxID)

	return nil
}
