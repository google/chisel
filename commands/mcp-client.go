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
	"time"

	"flag"
	"log"
	"github.com/google/subcommands"
	chiselmcp "chisel/mcp"
	"chisel/types"
)

// MCPClientCmd implements the subcommands interface for the "mcptestclient"
// subcommand.
type MCPClientCmd struct {
	commit  string
	path    string
	hunkID  string
	limit   int
	offset  int
	verbose bool
}

// Name returns the subcommand name.
func (s *MCPClientCmd) Name() string { return "mcp-client" }

// Synopsis describes the subcommand behavior.
func (s *MCPClientCmd) Synopsis() string {
	return "Exercise the MCP storage methods and display time duration and approximate token count of calls."
}

// Usage describes the subcommand usage.
func (s *MCPClientCmd) Usage() string {
	return `mcptestclient <flags> <method>

Supported methods:
  downstream_upstream_pairings
  most_recent_stats_commits
  most_recent_stats_commit
  most_recent_project_deltas
  project_time_series
  most_recent_file_deltas
  commit_bugs
  commit_deltas
  commit_file_deltas
  hunks_for_commit
  hunks_for_file
  hunk_chain
`
}

// SetFlags describes the subcommand flags.
func (s *MCPClientCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.commit, "commit", "", "Commit SHA for methods that require it.")
	f.StringVar(&s.path, "path", "", "File path for methods that require it.")
	f.StringVar(&s.hunkID, "hunk_id", "", "Hunk ID for methods that require it.")
	f.IntVar(&s.limit, "limit", 0, "Limit for pagination.")
	f.IntVar(&s.offset, "offset", 0, "Offset for pagination.")
	f.BoolVar(&s.verbose, "verbose", false, "Display the results of the query.")
}

// Execute forwards the flags and options to the appropriate handler.
func (s *MCPClientCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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
	if len(appConfig.Projects) > 0 {
		project = appConfig.Projects[0].Name
	}

	// Default storage from the config.
	storage, err := storageFactory(ctx, types.StorageConfig{
		StorageType:     appConfig.StorageType,
		StorageDir:      appConfig.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	defer storage.Close()

	// Factory to create new storages.
	factory := func(ctx context.Context, storageConfig types.StorageConfig) (chiselmcp.Storage, error) {
		store, err := storageFactory(ctx, storageConfig)
		if err != nil {
			return nil, err
		}
		return store.(chiselmcp.Storage), nil
	}

	serverImpl, err := chiselmcp.NewServer(factory)
	if err != nil {
		log.Printf("chiselmcp NewServer failed: %v", err)
		return subcommands.ExitFailure
	}
	serverImpl.AddStorage("default", storage.(chiselmcp.Storage))

	ud := chiselmcp.UpstreamDownstream{
		StorageName:      "default",
		UpstreamName:     appConfig.UpstreamName,
		UpstreamBranch:   appConfig.UpstreamBranch,
		DownstreamName:   appConfig.DownstreamName,
		DownstreamBranch: appConfig.DownstreamBranch,
	}

	udp := chiselmcp.UpstreamDownstreamProject{
		UpstreamDownstream: ud,
		Project:            project,
	}

	udop := chiselmcp.UpstreamDownstreamOptionalProject{
		UpstreamDownstream: ud,
		Project:            project,
	}

	var payload any

	start := time.Now()
	switch method {
	case "downstream_upstream_pairings":
		_, payload, err = serverImpl.DownstreamUpstreamPairings(ctx, nil, chiselmcp.StorageOnlyInput{StorageName: "default"})
	case "most_recent_stats_commits":
		_, payload, err = serverImpl.MostRecentStatsCommits(ctx, nil, ud)
	case "most_recent_stats_commit":
		_, payload, err = serverImpl.MostRecentStatsCommit(ctx, nil, udp)
	case "most_recent_project_deltas":
		_, payload, err = serverImpl.MostRecentProjectDeltas(ctx, nil, udop)
	case "project_time_series":
		_, payload, err = serverImpl.ProjectTimeSeries(ctx, nil, udp)
	case "most_recent_file_deltas":
		_, payload, err = serverImpl.MostRecentFileDeltas(ctx, nil, chiselmcp.UpstreamDownstreamProjectOptionalPath{
			UpstreamDownstreamProject: udp,
			Path:                      s.path,
		})
	case "commit_bugs":
		_, payload, err = serverImpl.CommitBugs(ctx, nil, chiselmcp.DownstreamProject{
			StorageName:    "default",
			DownstreamName: appConfig.DownstreamName,
			Project:        project,
		})
	case "commit_deltas":
		_, payload, err = serverImpl.CommitDeltas(ctx, nil, chiselmcp.UpstreamDownstreamProjectOptionalCommit{
			UpstreamDownstreamProject: udp,
			Commit:                    s.commit,
			Limit:                     s.limit,
			Offset:                    s.offset,
		})
	case "commit_file_deltas":
		_, payload, err = serverImpl.CommitFileDeltas(ctx, nil, chiselmcp.UpstreamDownstreamProjectPagination{
			UpstreamDownstreamProject: udp,
			Limit:                     s.limit,
			Offset:                    s.offset,
		})
	case "hunks_for_commit":
		_, payload, err = serverImpl.HunksForCommit(ctx, nil, chiselmcp.UpstreamDownstreamProjectCommit{
			UpstreamDownstreamProject: udp,
			Commit:                    s.commit,
			Limit:                     s.limit,
			Offset:                    s.offset,
		})
	case "hunks_for_file":
		_, payload, err = serverImpl.HunksForFile(ctx, nil, chiselmcp.UpstreamDownstreamProjectPath{
			UpstreamDownstreamProject: udp,
			Path:                      s.path,
		})
	case "hunk_chain":
		_, payload, err = serverImpl.HunkChain(ctx, nil, chiselmcp.HunkChainInput{
			UpstreamDownstreamProject: udp,
			HunkID:                    s.hunkID,
			Path:                      s.path,
		})
	default:
		log.Printf("unknown method %q", method)
		return subcommands.ExitFailure
	}

	if err != nil {
		log.Printf("Method %q failed: %v", method, err)
		return subcommands.ExitFailure
	}

	fmt.Printf("query completed in %s\n", time.Since(start))

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal payload: %v", err)
		return subcommands.ExitFailure
	}
	fmt.Printf("approximate token count: %d (based on 4 chars/token)\n", len(payloadBytes)/4)

	if s.verbose {
		if err := display(payload); err != nil {
			log.Print("Could not display the results")
			return subcommands.ExitFailure
		}
	}

	return subcommands.ExitSuccess
}
