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

// Chisel provides visibility into divergences between two git branches
// over time.
package main

import (
	"context"
	"encoding/gob"
	"io"
	"os"

	"flag"
	"github.com/google/subcommands"
	"chisel/commands"
	"chisel/flags"
	"chisel/types"

	_ "github.com/mattn/go-sqlite3"
	_ "chisel/repositories"
)

const (
	configHelp = `Path to application config file.`

	workDirHelp = `Path to the directory that stores chisel cache files.
	The local sqlite database is also stored here when --storage_type is "sqlite".`

	storageTypeHelp = `Storage type.
	Legal values are "sqlite" and "proxy". (default "sqlite")`


	downstreamHostHelp = `Full path to the downstream host, e.g. https://android.googlesource.com`

	downstreamHostNameHelp = `Short name for the host, e.g. aosp-downstream.`

	downstreamRemoteHelp = `Name of the git remote for the downstream.`

	downstreamBranchHelp = `Name of the downstream branch without the leading <remote>/.`

	upstreamHostHelp = `Full path to the upstream host, e.g. https://android.googlesource.com`

	upstreamHostNameHelp = `Short name for the host, e.g. aosp.`

	upstreamRemoteHelp = `Name of the git remote for the upstream.`

	upstreamBranchHelp = `Name of the upstream branch without the leading <remote>/.`

	projectsHelp = `List of project names. In some contexts, only the first
	project may be used.`
)

var (
	projects flags.MultiString

	cfg                = flag.String("c", "", configHelp)
	workDir            = flag.String("work_dir", "", workDirHelp)
	storageType        = flag.String("storage_type", "", storageTypeHelp)
	downstreamHost     = flag.String("downstream_host", "", downstreamHostHelp)
	downstreamHostName = flag.String("downstream_host_name", "", downstreamHostNameHelp)
	downstreamRemote   = flag.String("downstream_remote", "", downstreamRemoteHelp)
	downstreamBranch   = flag.String("downstream_branch", "", downstreamBranchHelp)
	upstreamHost       = flag.String("upstream_host", "", upstreamHostHelp)
	upstreamHostName   = flag.String("upstream_host_name", "", upstreamHostNameHelp)
	upstreamRemote     = flag.String("upstream_remote", "", upstreamRemoteHelp)
	upstreamBranch     = flag.String("upstream_branch", "", upstreamBranchHelp)
)

func registerSubcommands() {
	subcommands.Register(subcommands.HelpCommand(), "help")
	subcommands.Register(subcommands.CommandsCommand(), "help")
	subcommands.Register(subcommands.FlagsCommand(), "help")
	subcommands.Register(&commands.InitCmd{}, "")
	subcommands.Register(&commands.StatsCmd{}, "")
	subcommands.Register(&commands.ContentsCmd{}, "")
	subcommands.Register(&commands.ListCmd{}, "")
	subcommands.Register(&commands.InteractiveCmd{}, "")
	subcommands.Register(&commands.ServeCmd{}, "")
	subcommands.Register(&commands.VersionCmd{}, "")
	subcommands.Register(&commands.LicenseCmd{}, "")
	subcommands.Register(&commands.CleanCmd{}, "")
	subcommands.Register(&commands.DumpConfigCmd{}, "")
	subcommands.Register(&commands.StorageClientCmd{}, "")
	subcommands.Register(&commands.ManifestCmd{}, "")
	subcommands.Register(&commands.GCCmd{}, "")
	subcommands.Register(&commands.GrafanaCmd{}, "")
	subcommands.Register(&commands.MCPCmd{}, "")
	subcommands.Register(&commands.MCPClientCmd{}, "")
	flag.VisitAll(func(f *flag.Flag) {
		if true {
			subcommands.ImportantFlag(f.Name)
		}
	})
}

func main() {
	registerSubcommands()
	flag.Var(&projects, "projects", projectsHelp)
	flag.Parse()

	overrideConfig := &types.ApplicationConfig{
		WorkDir:          *workDir,
		StorageType:      *storageType,
		DownstreamHost:   *downstreamHost,
		DownstreamName:   *downstreamHostName,
		DownstreamRemote: *downstreamRemote,
		DownstreamBranch: *downstreamBranch,
		UpstreamHost:     *upstreamHost,
		UpstreamName:     *upstreamHostName,
		UpstreamRemote:   *upstreamRemote,
		UpstreamBranch:   *upstreamBranch,
	}
	overrideConfig.Projects = make([]types.NormalizedProjectConfig, 0, len(projects))
	for _, project := range projects {
		overrideConfig.Projects = append(overrideConfig.Projects, types.NormalizedProjectConfig{Name: project})
	}
	forceDeterministicEncodes()

	opts := &commands.Opts{
		ConfigFile:     *cfg,
		OverrideConfig: overrideConfig,
	}
	os.Exit(int(subcommands.Execute(context.Background(), opts)))
}

// Pain.
// https://stackoverflow.com/questions/70477328/two-gob-encoders-produce-different-results
func forceDeterministicEncodes() {
	discardType := func(t any) {
		gob.NewEncoder(io.Discard).Encode(t)
	}

	discardType(struct {
		ProjectConfig *types.ProjectConfig
		HeadCommit    string
		SinceCommit   string
		MaxCommits    int
	}{})

	discardType(struct {
		Host       string
		Project    string
		Downstream string
		Upstream   string
	}{})

	discardType(struct {
		ProjectConfig *types.ProjectConfig
		HeadCommit    string
		SinceCommit   string
		SkipCommits   []string
		MaxCommits    int
	}{})
}
