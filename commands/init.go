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

// Package commands provides the subcommand interface functions for
// subcommands usage.
package commands

import (
	"context"

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/handlers"
)

// InitCmd implements the subcommands interface for the "init"
// subcommand.
type InitCmd struct {
}

// Name returns the subcommand name.
func (i *InitCmd) Name() string { return "init" }

// Synopsis describes the subcommand behavior.
func (i *InitCmd) Synopsis() string {
	return "Initialize either a 'repo' or 'git' workarea."
}

// Usage describes the subcommand usage.
func (i *InitCmd) Usage() string {
	return "init <flags>\n"
}

// SetFlags describes the subcommand flags.
func (i *InitCmd) SetFlags(f *flag.FlagSet) {
}

// Execute forwards the flags and options to the appropriate handler.
func (i *InitCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}

	o := opts[0].(*Opts)
	config, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	initializeOptions := &handlers.InitializeOptions{
		ApplicationConfig: config,
		GitClientCreator:  gitClientCreator,
	}

	if err := handlers.Initialize(ctx, initializeOptions); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
