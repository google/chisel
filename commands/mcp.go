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
	"fmt"
	"os"

	"flag"
	"log"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/google/subcommands"
	chiselmcp "chisel/mcp"
	"chisel/types"
)

// MCPCmd implements the subcommands interface for the "mcp"
// subcommand.
type MCPCmd struct {
	logPath string
}

// Name returns the subcommand name.
func (m *MCPCmd) Name() string { return "mcp" }

// Synopsis describes the subcommand behavior.
func (m *MCPCmd) Synopsis() string {
	return "Start the Chisel MCP server."
}

// Usage describes the subcommand usage.
func (m *MCPCmd) Usage() string {
	return "mcp\n"
}

// SetFlags describes the subcommand flags.
func (m *MCPCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&m.logPath, "l", "", "Log file. An empty value is treated as stderr.")
}

// Execute forwards the flags and options to the appropriate handler.
func (m *MCPCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}

	o := opts[0].(*Opts)
	appConfig, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	logFile, close, err := outOrStderr(m.logPath)
	if err != nil {
		log.Printf("outOrStderr failed: %v", err)
		return subcommands.ExitFailure
	}
	defer close()

	// This line gets replaced as part of the pubify flow.
	log.SetOutput(logFile)

	log.Print("Chisel MCP server started...")
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
	defer serverImpl.Close()

	// Register a default storage if it was provided through the config.
	if appConfig.StorageType != "" {
		serverImpl.AddStorageConfig("default", types.StorageConfig{
			StorageType:     appConfig.StorageType,
			StorageDir:      appConfig.WorkDir,
		})
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "chisel", Version: "0.0.1"}, nil)

	if err := m.addTools(server, serverImpl); err != nil {
		log.Printf("Failed to add tools: %v", err)
		return subcommands.ExitFailure
	}

	log.Print("Launching server loop...")
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("Server error: %v", err)
		return subcommands.ExitFailure
	}

	log.Print("Chisel MCP server stopped.")

	return subcommands.ExitSuccess
}

func outOrStderr(outputPath string) (*os.File, func() error, error) {
	if outputPath == "" {
		return os.Stderr, func() error { return nil }, nil
	}

	f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to open file %q. err: %v", outputPath, err)
	}

	return f, func() error { return f.Close() }, nil
}

func (m *MCPCmd) addTools(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	if err := m.addMostRecentStatsCommits(server, serverImpl); err != nil {
		return err
	}

	if err := m.addDownstreamUpstreamPairings(server, serverImpl); err != nil {
		return err
	}

	if err := m.addMostRecentStatsCommit(server, serverImpl); err != nil {
		return err
	}

	if err := m.addMostRecentProjectDeltas(server, serverImpl); err != nil {
		return err
	}

	if err := m.addProjectTimeSeries(server, serverImpl); err != nil {
		return err
	}

	if err := m.addMostRecentFileDeltas(server, serverImpl); err != nil {
		return err
	}

	if err := m.addCommitBugs(server, serverImpl); err != nil {
		return err
	}

	if err := m.addCommitDeltas(server, serverImpl); err != nil {
		return err
	}

	if err := m.addCommitFileDeltas(server, serverImpl); err != nil {
		return err
	}

	if err := m.addHunksForCommit(server, serverImpl); err != nil {
		return err
	}

	if err := m.addHunksForFile(server, serverImpl); err != nil {
		return err
	}

	if err := m.addHunkChain(server, serverImpl); err != nil {
		return err
	}

	if err := m.addRegisterStorage(server, serverImpl); err != nil {
		return err
	}

	return nil
}

func (m *MCPCmd) addRegisterStorage(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.RegisterStorageInput](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "register_storage",
		Description: "Registers a new storage with the pool.",
		InputSchema: schema,
	}, serverImpl.RegisterStorage)

	return nil
}

func (m *MCPCmd) addDownstreamUpstreamPairings(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.StorageOnlyInput](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "downstream_upstream_pairings",
		Description: "Lists all supported downstream/upstream pairings found in the store.",
		InputSchema: schema,
	}, serverImpl.DownstreamUpstreamPairings)

	return nil
}

func (m *MCPCmd) addMostRecentStatsCommits(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstream](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "most_recent_stats_commits",
		Description: "Lists the most recent commit for all projects for which a Chisel Stats analysis are available.",
		InputSchema: schema,
	}, serverImpl.MostRecentStatsCommits)

	return nil
}

func (m *MCPCmd) addMostRecentStatsCommit(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProject](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "most_recent_stats_commit",
		Description: "Shows the most recent commit and metadata for an input project for which a Chisel Stats analysis is available.",
		InputSchema: schema,
	}, serverImpl.MostRecentStatsCommit)

	return nil
}

func (m *MCPCmd) addMostRecentProjectDeltas(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamOptionalProject](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "most_recent_project_deltas",
		Description: "Lists the project deltas at the most recently analyzed commit. If a project isn't specified, deltas are returned for all projects.",
		InputSchema: schema,
	}, serverImpl.MostRecentProjectDeltas)

	return nil
}

func (m *MCPCmd) addProjectTimeSeries(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProject](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "project_time_series",
		Description: "Lists the project time series data to show how a project changes over time.",
		InputSchema: schema,
	}, serverImpl.ProjectTimeSeries)

	return nil
}

func (m *MCPCmd) addMostRecentFileDeltas(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProjectOptionalPath](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "most_recent_file_deltas",
		Description: "Lists the file deltas for a project at the most recently analyzed commit. This tool supports pagination. If a file path isn't specified, deltas are returned for all files.",
		InputSchema: schema,
	}, serverImpl.MostRecentFileDeltas)

	return nil
}

func (m *MCPCmd) addCommitBugs(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.DownstreamProject](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "commit_bugs",
		Description: "Shows the bug(s) or issue ID(s) associated with commits for a given project.",
		InputSchema: schema,
	}, serverImpl.CommitBugs)

	return nil
}

func (m *MCPCmd) addCommitDeltas(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProjectOptionalCommit](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "commit_deltas",
		Description: "Shows the commit delta information for the largest delta commits. This tool supports pagination. If a commit is specified, only the delta for that commit are returned.",
		InputSchema: schema,
	}, serverImpl.CommitDeltas)

	return nil
}

func (m *MCPCmd) addCommitFileDeltas(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProjectPagination](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "commit_file_deltas",
		Description: "Shows the commit file delta information. This tool supports pagination.",
		InputSchema: schema,
	}, serverImpl.CommitFileDeltas)

	return nil
}

func (m *MCPCmd) addHunksForCommit(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProjectCommit](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "hunks_for_commit",
		Description: "Lists all hunks for a project at a specific commit.",
		InputSchema: schema,
	}, serverImpl.HunksForCommit)

	return nil
}

func (m *MCPCmd) addHunksForFile(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.UpstreamDownstreamProjectPath](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "hunks_for_file",
		Description: "Lists all hunks for a file in a project.",
		InputSchema: schema,
	}, serverImpl.HunksForFile)

	return nil
}

func (m *MCPCmd) addHunkChain(server *mcp.Server, serverImpl *chiselmcp.Server) error {
	schema, err := jsonschema.For[chiselmcp.HunkChainInput](nil)
	if err != nil {
		return fmt.Errorf("schema error: %v", err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "hunk_chain",
		Description: "Traces the chain of hunks starting from a specific hunk ID.",
		InputSchema: schema,
	}, serverImpl.HunkChain)

	return nil
}
