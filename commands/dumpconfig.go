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
	"github.com/google/subcommands"
	"chisel/config"
	"chisel/types"
)

// DumpConfigCmd implements the subcommands interface for the "dumpconfig"
// subcommand.
type DumpConfigCmd struct {
	beforeCommit string
	file         string
	useContext   bool
}

// Name returns the subcommand name.
func (d *DumpConfigCmd) Name() string { return "dumpconfig" }

// Synopsis describes the subcommand behavior.
func (d *DumpConfigCmd) Synopsis() string {
	return "Dump the original and overridden configs"
}

// Usage describes the subcommand usage.
func (d *DumpConfigCmd) Usage() string {
	return "dumpconfig <flags>"
}

// SetFlags describes the subcommand flags.
func (d *DumpConfigCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&d.beforeCommit, "before_commit", "", interactiveBeforeCommitHelp)
	f.StringVar(&d.file, "file", "", interactiveFileHelp)
	f.BoolVar(&d.useContext, "use_context", true, useContextHelp)
}

func dumpOriginalConfig(configFile string) error {
	initialConfig, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("couldn't read or find a config file: %v", err)
	}
	return config.WriteConfig(initialConfig, os.Stdout)
}

func dumpOverriddenConfig(o *Opts) (*types.ApplicationConfig, error) {
	appConfig, err := loadConfigWithOverrides(o)
	if err != nil {
		return nil, fmt.Errorf("loadConfigWithOverrides failed: %v", err)
	}

	return appConfig, config.WriteConfig(appConfig, os.Stdout)
}

func dumpQueryConfig(queryConfig *types.QueryConfig) {
	fmt.Println("BeforeCommit: ", queryConfig.BeforeCommit)
	fmt.Println("File: ", queryConfig.File)
}

// Execute forwards the flags and options to the appropriate handler.
func (d *DumpConfigCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}
	o := opts[0].(*Opts)

	fmt.Println("Original Application Config")
	fmt.Println("---------------------------")
	if err := dumpOriginalConfig(o.ConfigFile); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}
	fmt.Println("")

	fmt.Println("Overridden Application Config")
	fmt.Println("-----------------------------")
	appConfig, err := dumpOverriddenConfig(o)
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}
	fmt.Println("")

	queryConfig := &types.QueryConfig{
		ApplicationConfig: appConfig,
		BeforeCommit:      d.beforeCommit,
		File:              d.file,
	}
	fmt.Println("Original Query Config")
	fmt.Println("---------------------")
	dumpQueryConfig(queryConfig)
	fmt.Println("")

	if !d.useContext {
		return subcommands.ExitSuccess
	}

	if d.useContext {
		appConfig, queryConfig, err = applyContextOverrides(appConfig, queryConfig)
		if err != nil {
			fmt.Println("Could not infer context, so skipping further config prints")
			return subcommands.ExitSuccess
		}
	}

	fmt.Println("Context Overridden Application Config")
	fmt.Println("------------------------------------")
	config.WriteConfig(appConfig, os.Stdout)
	fmt.Println("")

	fmt.Println("Context Overridden Query Config")
	fmt.Println("-------------------------------")
	dumpQueryConfig(queryConfig)
	fmt.Println("")

	return subcommands.ExitSuccess
}
