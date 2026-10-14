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

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/handlers"
	"chisel/types"
)

const (
	beforeCommitHelp = `Upper inclusive bound on commits to inspect for "commits|files|hunks|dag".`

	commitHelp = `Commit to inspect for "hunks|files|commit". Takes precedence over -before_commit.`

	lineHelp = `Line to inspect for "dag". "before_commit" must also be specified
	explicitly or inferred through context. -hunk_id takes precedence.`

	hunkIDHelp = `Hunk to inspect for "hunk|dag".`

	listFileHelp = `File to inspect for "hunks|hunk|dag".`

	useContextHelp = `When true, the state of the current "git"
	directory is used for "list" and "interactive" commands.
	Otherwise, the latest analyzed information is used.`
)

// ListCmd implements the subcommands interface for the "list"
// subcommand.
type ListCmd struct {
	beforeCommit string
	commit       string
	hunkID       string
	line         int
	file         string
	useContext   bool
}

// Name returns the subcommand name.
func (l *ListCmd) Name() string { return "list" }

// Synopsis describes the subcommand behavior.
func (l *ListCmd) Synopsis() string {
	return "List requested information present in existing database. Note that not all subcommands are supported for proxy storage."
}

// Usage describes the subcommand usage.
func (l *ListCmd) Usage() string {
	return "list <flags> <commit|commits|files|hunk|hunks|dag|latest_contents_commit|latest_stats_commit|latest_contents_commits|latest_stats_commits>\n"
}

// SetFlags describes the subcommand flags.
func (l *ListCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&l.beforeCommit, "before_commit", "", beforeCommitHelp)
	f.StringVar(&l.commit, "commit", "", commitHelp)
	f.StringVar(&l.hunkID, "hunk_id", "", hunkIDHelp)
	f.IntVar(&l.line, "line", 0, lineHelp)
	f.StringVar(&l.file, "file", "", listFileHelp)
	f.BoolVar(&l.useContext, "use_context", true, useContextHelp)
}

// Execute forwards the flags and options to the appropriate handler.
func (l *ListCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	args := f.Args()
	if len(args) == 0 {
		log.Printf("Improper usage. Usage: %v", l.Usage())
		return subcommands.ExitUsageError
	}
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}

	action := args[0]
	o := opts[0].(*Opts)
	appConfig, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	queryConfig := &types.QueryConfig{
		ApplicationConfig: appConfig,
		BeforeCommit:      l.beforeCommit,
		Commit:            l.commit,
		HunkID:            l.hunkID,
		Line:              l.line,
		File:              l.file,
	}
	if l.useContext {
		appConfig, queryConfig, err = applyContextOverrides(appConfig, queryConfig)
		if err != nil {
			log.Printf("applyContextOverrides failed: %v", err)
			return subcommands.ExitFailure
		}
	}

	storage, err := storageFactory(ctx, types.StorageConfig{
		StorageType:     appConfig.StorageType,
		StorageDir:      appConfig.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	defer storage.Close()

	if err := handlers.List(ctx, action, queryConfig, storage, dataSink()); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
