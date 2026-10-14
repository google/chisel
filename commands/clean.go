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
	"os"
	"path"

	"flag"
	"log"
	"golang.org/x/sync/errgroup"
	"github.com/google/subcommands"
	"chisel/constants"
)

// CleanCmd implements the subcommands interface for the "clean"
// subcommand.
type CleanCmd struct {
	cleanRepos   bool
	cleanCache   bool
	cleanSqlite  bool
	cleanGrafana bool
	cleanAll     bool
}

// Name returns the subcommand name.
func (c *CleanCmd) Name() string { return "clean" }

// Synopsis describes the subcommand behavior.
func (c *CleanCmd) Synopsis() string {
	return "Clean the chisel work directory."
}

// Usage describes the subcommand usage.
func (c *CleanCmd) Usage() string {
	return "clean <flags>"
}

// SetFlags describes the subcommand flags.
func (c *CleanCmd) SetFlags(f *flag.FlagSet) {
	f.BoolVar(&c.cleanRepos, "r", false, "Clean the repos area.")
	f.BoolVar(&c.cleanCache, "c", false, "Clean the cache area.")
	f.BoolVar(&c.cleanSqlite, "s", false, "Clean the sqlite area.")
	f.BoolVar(&c.cleanGrafana, "g", false, "Clean the grafana area.")
	f.BoolVar(&c.cleanAll, "a", false, "Clean the entire work directory.")
}

// Execute forwards the flags and options to the appropriate handler.
func (c *CleanCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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
	if config.WorkDir == "" {
		log.Print("work directory is empty")
		return subcommands.ExitFailure
	}

	if c.cleanAll {
		c.cleanCache = true
		c.cleanRepos = true
		c.cleanSqlite = true
		c.cleanGrafana = true
	}

	var eg errgroup.Group

	if c.cleanCache {
		eg.Go(func() error {
			return os.RemoveAll(path.Join(config.WorkDir, constants.CacheDir))
		})
	}

	if c.cleanRepos {
		eg.Go(func() error {
			return os.RemoveAll(path.Join(config.WorkDir, constants.ReposDir))
		})
	}

	if c.cleanSqlite {
		eg.Go(func() error {
			return os.RemoveAll(path.Join(config.WorkDir, constants.SqliteDir))
		})
	}

	if c.cleanGrafana {
		eg.Go(func() error {
			return os.RemoveAll(path.Join(config.WorkDir, constants.GrafanaDir))
		})
	}

	if err := eg.Wait(); err != nil {
		log.Printf("cleaning failed: %v", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
