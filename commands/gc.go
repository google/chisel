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
	"path/filepath"
	"runtime"

	"flag"
	"log"
	"golang.org/x/sync/errgroup"
	"github.com/google/subcommands"
	"chisel/constants"
	"chisel/types"
)

// GCCmd implements the subcommands interface for the "gc"
// subcommand.
type GCCmd struct {
}

// Name returns the subcommand name.
func (i *GCCmd) Name() string { return "gc" }

// Synopsis describes the subcommand behavior.
func (i *GCCmd) Synopsis() string {
	return "Run git garbage collection on local repos"
}

// Usage describes the subcommand usage.
func (i *GCCmd) Usage() string {
	return "gc\n"
}

// SetFlags describes the subcommand flags.
func (i *GCCmd) SetFlags(f *flag.FlagSet) {
}

// Execute forwards the flags and options to the appropriate handler.
func (i *GCCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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

	if err := gcProjects(config); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

func gcProjects(config *types.ApplicationConfig) error {
	eg := new(errgroup.Group)
	eg.SetLimit(runtime.NumCPU())

	for _, project := range config.Projects {
		path := filepath.Join(config.WorkDir, constants.ReposDir, project.Path())
		gitClient := gitClientCreator(path, 1)

		eg.Go(func() error {
			if err := gitClient.GarbageCollect(); err != nil {
				log.Printf("Unable to garbage collect %q : %v", path, err)
				return err
			}

			return nil
		})
	}

	return eg.Wait()
}
