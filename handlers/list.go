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
	"context"
	"errors"
	"fmt"
	"strconv"

	"chisel/constants"
	"chisel/storage"
	"chisel/types"
)

// DAG modes.
const (
	unsetMode int = iota
	hunkMode
	lineMode
)

func checkConfig(applicationConfig *types.ApplicationConfig, action string) error {
	allowEmptyProject := (action == constants.LatestContentsCommits || action == constants.LatestStatsCommits)
	if !allowEmptyProject {
		if len(applicationConfig.Projects) != 1 {
			return fmt.Errorf("expected a single project but received %v", len(applicationConfig.Projects))
		}
		if applicationConfig.Projects[0].Name == "" {
			return errors.New("project is not set")
		}
	}

	if applicationConfig.DownstreamBranch == "" {
		return errors.New("downstream is not set")
	}

	if applicationConfig.UpstreamBranch == "" {
		return errors.New("upstream is not set")
	}

	return nil
}

// List will display information from storage for the input action.
// Note: SortedHunksForFile is a potential candidate for performance
// improvement. It is not always the case that all returned hunks are desired.
// For those scenarios, it would be beneficial for queries to refrain from
// reading the content of all the hunks, and only load the full content
// for hunks of interest.
func List(ctx context.Context, action string, queryConfig *types.QueryConfig, storage storage.Storage, sink DataSinker) error {
	if err := checkConfig(queryConfig.ApplicationConfig, action); err != nil {
		return err
	}

	projectConfig := &types.ProjectConfig{
		DownstreamHost:   queryConfig.ApplicationConfig.DownstreamHost,
		DownstreamName:   queryConfig.ApplicationConfig.DownstreamName,
		DownstreamBranch: queryConfig.ApplicationConfig.DownstreamBranch,
		DownstreamRemote: queryConfig.ApplicationConfig.DownstreamRemote,
		UpstreamHost:     queryConfig.ApplicationConfig.UpstreamHost,
		UpstreamName:     queryConfig.ApplicationConfig.UpstreamName,
		UpstreamBranch:   queryConfig.ApplicationConfig.UpstreamBranch,
		UpstreamRemote:   queryConfig.ApplicationConfig.UpstreamRemote,
		Name:             queryConfig.ApplicationConfig.Projects[0].Name,
	}
	switch action {
	case constants.LatestContentsCommits:
		commits, err := storage.MostRecentContentsCommits(ctx, projectConfig)
		if err != nil {
			return err
		}
		return sink.LatestCommits(commits)
	case constants.LatestStatsCommits:
		commits, err := storage.MostRecentStatsCommits(ctx, projectConfig)
		if err != nil {
			return err
		}
		return sink.LatestCommits(commits)
	case constants.LatestContentsCommit:
		commit, err := storage.MostRecentContentsCommit(ctx, projectConfig)
		if err != nil {
			return err
		}
		return sink.LatestCommit(commit.Commit)
	case constants.LatestStatsCommit:
		commit, err := storage.MostRecentStatsCommit(ctx, projectConfig)
		if err != nil {
			return err
		}
		return sink.LatestCommit(commit.Commit)
	case constants.CommitAction:
		commit, err := storage.Commit(ctx, queryConfig)
		if err != nil {
			return err
		}
		return sink.Commit(commit)
	case constants.CommitsAction:
		sortedCommits, err := storage.SortedCommits(ctx, queryConfig)
		if err != nil {
			return err
		}
		return sink.Commits(sortedCommits)
	case constants.FilesAction:
		sortedFiles, err := storage.SortedFiles(ctx, queryConfig)
		if err != nil {
			return err
		}
		return sink.Files(sortedFiles)
	case constants.HunkAction:
		if queryConfig.File == "" {
			return errors.New("file is not set")
		}

		hunkListItem, err := storage.HunkByID(ctx, queryConfig)
		if err != nil {
			return err
		}
		return sink.Hunk(hunkListItem)
	case constants.HunksAction:
		if queryConfig.File == "" {
			return errors.New("file is not set")
		}

		hunkListItems, err := storage.SortedHunksForFile(ctx, queryConfig)
		if err != nil {
			return err
		}
		return sink.Hunks(hunkListItems)
	case constants.DAGAction:
		if queryConfig.File == "" {
			return errors.New("file is not set")
		}

		if queryConfig.Commit != "" {
			return errors.New("commit should not be specified for dag command")
		}

		mode := unsetMode
		if queryConfig.HunkID != "" {
			mode = hunkMode
		} else if queryConfig.BeforeCommit != "" && queryConfig.Line > 0 {
			mode = lineMode
		} else {
			return errors.New("'dag' command requires HunkID or (BeforeCommit, Line) to be specified")
		}

		var id int64
		if mode == hunkMode {
			var err error
			id, err = strconv.ParseInt(queryConfig.HunkID, 10, 64)
			if err != nil {
				return fmt.Errorf("failed to parse applicationConfig.HunkID: %v", err)
			}
		}

		hunkListItems, err := storage.SortedHunksForFile(ctx, queryConfig)
		if err != nil {
			return err
		}
		views := generateViews(hunkListItems)

		switch mode {
		case hunkMode:
			return sink.DAG(views.dagForItem(id))
		case lineMode:
			return sink.DAG(views.dagForLine(queryConfig.Line))
		}
	default:
		return fmt.Errorf("unknown action %q", action)
	}

	return nil
}
