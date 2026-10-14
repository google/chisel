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

// ServeCmd implements the subcommands interface for the "serve"
// subcommand.
type ServeCmd struct {
}

// Name returns the subcommand name.
func (l *ServeCmd) Name() string { return "serve" }

// Synopsis describes the subcommand behavior.
func (l *ServeCmd) Synopsis() string {
	return "Serve a persistent connection to a database."
}

// Usage describes the subcommand usage.
func (l *ServeCmd) Usage() string {
	return "serve"
}

// SetFlags describes the subcommand flags.
func (l *ServeCmd) SetFlags(f *flag.FlagSet) {}

// Execute forwards the flags and options to the appropriate handler.
func (l *ServeCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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

	storage, err := storageFactory(ctx, types.StorageConfig{
		StorageType:     config.StorageType,
		StorageDir:      config.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	defer storage.Close()

	if err := handlers.Serve(ctx, config, storage); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
