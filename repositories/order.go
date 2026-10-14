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
	"math"

	"chisel/contents"
	"chisel/types"
)

type commitsStore interface {
	Commit(ctx context.Context, queryConfig *types.QueryConfig) (*contents.CommitListItem, error)
	SortedCommits(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.CommitListItem, error)
}

func maxOrderIDBeforeCommit(ctx context.Context, s commitsStore, queryConfig *types.QueryConfig) (int64, error) {
	commitReqConfig := *queryConfig
	commitReqConfig.Commit = queryConfig.BeforeCommit
	maxCommit, err := s.Commit(ctx, &commitReqConfig)
	if err != nil {
		return 0, err
	}
	// When the commit isn't found, don't apply any filtering on order ID.
	if maxCommit == nil {
		return math.MaxInt64, nil
	}

	return maxCommit.OrderID, nil
}
