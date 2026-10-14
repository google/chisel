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
	interactiveBeforeCommitHelp = `Upper inclusive bound on commits to inspect.`
	interactiveFileHelp         = `File to inspect for "interactive".`
)

// InteractiveCmd implements the subcommands interface for the "interactive"
// subcommand.
type InteractiveCmd struct {
	beforeCommit string
	file         string
	useContext   bool
}

// Name returns the subcommand name.
func (i *InteractiveCmd) Name() string { return "interactive" }

// Synopsis describes the subcommand behavior.
func (i *InteractiveCmd) Synopsis() string {
	return "Simple command-line tool to inspect successor chains."
}

// Usage describes the subcommand usage.
func (i *InteractiveCmd) Usage() string {
	return "interactive <flags>\n"
}

// SetFlags describes the subcommand flags.
func (i *InteractiveCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&i.beforeCommit, "before_commit", "", interactiveBeforeCommitHelp)
	f.StringVar(&i.file, "file", "", interactiveFileHelp)
	f.BoolVar(&i.useContext, "use_context", true, useContextHelp)
}

// Execute forwards the flags and options to the appropriate handler.
func (i *InteractiveCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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

	queryConfig := &types.QueryConfig{
		ApplicationConfig: appConfig,
		BeforeCommit:      i.beforeCommit,
		File:              i.file,
	}
	if i.useContext {
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

	if err := handlers.Interact(ctx, queryConfig, storage); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
