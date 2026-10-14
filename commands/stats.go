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
	"path"

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/cache"
	"chisel/constants"
	"chisel/handlers"
	"chisel/stats"
	"chisel/types"
)

const (
	emailMapperHelp = `Custom email mapper to use.
	Default mapper strips usernames and retains only @domain.`
)

// StatsCmd implements the subcommands interface for the "stats"
// subcommand.
type StatsCmd struct {
	emailMapper string
}

// Name returns the subcommand name.
func (s *StatsCmd) Name() string { return "stats" }

// Synopsis describes the subcommand behavior.
func (s *StatsCmd) Synopsis() string {
	return "Collect diff stats on an already initialized 'repo' or 'git' workarea."
}

// Usage describes the subcommand usage.
func (s *StatsCmd) Usage() string {
	return "stats <flags>\n"
}

// SetFlags describes the subcommand flags.
func (s *StatsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&s.emailMapper, "email_mapper", "", emailMapperHelp)
}

// Execute forwards the flags and options to the appropriate handler.
func (s *StatsCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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

	storage, err := storageFactory(context.Background(), types.StorageConfig{
		StorageType:     config.StorageType,
		StorageDir:      config.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	defer storage.Close()

	fileCache, err := cache.New(path.Join(config.WorkDir, constants.CacheDir))
	if err != nil {
		log.Fatalf("Couldn't create file cache: %v", err)
		return subcommands.ExitFailure
	}
	var writeAheadCache handlers.Cache
	statsOptions := &handlers.StatsOptions{
		ApplicationConfig: config,
		Cache:             fileCache,
		WriteAheadCache:   writeAheadCache,
		Storage:           storage,
		GitClientCreator:  gitClientCreator,
		StatsDifferCreator: func(opts *stats.DifferOpts) (handlers.StatsDiffer, error) {
			return stats.New(opts)
		},
	}

	mapper, err := emailMapperFactory(s.emailMapper)
	if err != nil {
		log.Printf("emailMapperFactory : %v", err)
		return subcommands.ExitFailure
	}
	statsOptions.EmailMapper = mapper

	if err := handlers.Stats(ctx, statsOptions); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
