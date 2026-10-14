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

package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/types"
)

// StorageClientCmd implements the subcommands interface for the "storage-client"
// subcommand.
type StorageClientCmd struct {
	beforeCommit string
	commit       string
	hunkID       string
	file         string
	verbose      bool
}

// Name returns the subcommand name.
func (s *StorageClientCmd) Name() string { return "storage-client" }

// Synopsis describes the subcommand behavior.
func (s *StorageClientCmd) Synopsis() string {
	return "Exercise the storage API and display time duration of calls."
}

// Usage describes the subcommand usage.
func (s *StorageClientCmd) Usage() string {
	return `storage-client <flags> <method>

Supported methods:
  most_recent_stats_commits
  most_recent_stats_commit
  most_recent_files
  curr_commits
  all_hashes
  most_recent_contents_commit
  most_recent_contents_commits
  most_recent_live_hunks
  hunk_by_id
  sorted_hunks_for_file
  sorted_files
  commit
  sorted_commits
`
}

// SetFlags describes the subcommand flags.
func (s *StorageClientCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.beforeCommit, "before_commit", "", beforeCommitHelp)
	f.StringVar(&s.commit, "commit", "", commitHelp)
	f.StringVar(&s.hunkID, "hunk_id", "", hunkIDHelp)
	f.StringVar(&s.file, "file", "", listFileHelp)
	f.BoolVar(&s.verbose, "verbose", false, "Display the results of the query")
}

// Execute forwards the flags and options to the appropriate handler.
func (s *StorageClientCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	args := f.Args()
	if len(args) == 0 {
		log.Printf("Improper usage. Usage: %v", s.Usage())
		return subcommands.ExitUsageError
	}
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}

	method := args[0]
	o := opts[0].(*Opts)
	appConfig, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	var project string
	emptyProjectOK := method == "most_recent_stats_commits" || method == "most_recent_contents_commits"
	if len(appConfig.Projects) == 0 {
		if !emptyProjectOK {
			log.Print("The Projects configuration is empty")
			return subcommands.ExitFailure
		}
	} else {
		project = appConfig.Projects[0].Name
	}

	storage, err := storageFactory(ctx, types.StorageConfig{
		StorageType:     appConfig.StorageType,
		StorageDir:      appConfig.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	projConfig := &types.ProjectConfig{
		DownstreamName:   appConfig.DownstreamName,
		Name:             project,
		DownstreamBranch: appConfig.DownstreamBranch,
		UpstreamName:     appConfig.UpstreamName,
		UpstreamBranch:   appConfig.UpstreamBranch,
	}
	queryConfig := &types.QueryConfig{
		ApplicationConfig: appConfig,
		BeforeCommit:      s.beforeCommit,
		Commit:            s.commit,
		HunkID:            s.hunkID,
		File:              s.file,
	}
	var res any
	switch method {
	case "most_recent_stats_commit":
		start := time.Now()
		res, err = storage.MostRecentStatsCommit(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentStatsCommit err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "most_recent_stats_commits":
		start := time.Now()
		res, err = storage.MostRecentStatsCommits(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentStatsCommits err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "most_recent_files":
		start := time.Now()
		res, err = storage.MostRecentFiles(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentFiles err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "curr_commits":
		start := time.Now()
		res, err = storage.CurrCommits(ctx, projConfig)
		if err != nil {
			log.Printf("CurrCommits err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "all_hashes":
		start := time.Now()
		res, err = storage.AllHashes(ctx)
		if err != nil {
			log.Printf("AllHashes err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "most_recent_contents_commit":
		start := time.Now()
		res, err = storage.MostRecentContentsCommit(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentContentsCommit err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "most_recent_contents_commits":
		start := time.Now()
		res, err = storage.MostRecentContentsCommits(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentContentsCommits err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "most_recent_live_hunks":
		start := time.Now()
		res, err = storage.MostRecentLiveHunks(ctx, projConfig)
		if err != nil {
			log.Printf("MostRecentLiveHunks err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "hunk_by_id":
		start := time.Now()
		res, err = storage.HunkByID(ctx, queryConfig)
		if err != nil {
			log.Printf("HunkByID err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "sorted_hunks_for_file":
		start := time.Now()
		res, err = storage.SortedHunksForFile(ctx, queryConfig)
		if err != nil {
			log.Printf("SortedHunksForFile err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "sorted_files":
		start := time.Now()
		res, err = storage.SortedFiles(ctx, queryConfig)
		if err != nil {
			log.Printf("SortedFiles err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "commit":
		start := time.Now()
		res, err = storage.Commit(ctx, queryConfig)
		if err != nil {
			log.Printf("Commit err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	case "sorted_commits":
		start := time.Now()
		res, err = storage.SortedCommits(ctx, queryConfig)
		if err != nil {
			log.Printf("SortedCommits err: %v", err)
			return subcommands.ExitFailure
		}
		fmt.Printf("query completed in %s\n", time.Since(start))
	default:
		log.Printf("unknown method %q", method)
		return subcommands.ExitFailure
	}
	if s.verbose {
		if err := display(res); err != nil {
			log.Print("Could not display the results")
			return subcommands.ExitFailure
		}
	}

	return subcommands.ExitSuccess
}

func display(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}
