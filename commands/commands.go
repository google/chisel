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
	"path/filepath"

	"github.com/mattn/go-isatty"
	"chisel/config"
	"chisel/constants"
	"chisel/datasinks"
	"chisel/diff"
	"chisel/environment"
	"chisel/exec"
	"chisel/git"
	"chisel/handlers"
	"chisel/storage"
	"chisel/types"
)

// Opts provides options for configuring the implementations of subcommands
// interfaces.
type Opts struct {
	ConfigFile     string
	OverrideConfig *types.ApplicationConfig
}

func storageFactory(ctx context.Context, cfg types.StorageConfig) (storage.Storage, error) {
	storageType := cfg.StorageType
	workDir := cfg.StorageDir
	builder, err := storage.Lookup(storageType)
	if err != nil {
		return nil, err
	}
	var store storage.Storage

	switch storageType {
	case "sqlite":
		dbPath := filepath.Join(workDir, constants.SqliteDir, constants.SqliteDBFile)
		if err := os.MkdirAll(filepath.Dir(dbPath), 0750); err != nil {
			return nil, err
		}
		store, err = builder(dbPath)
		if err != nil {
			return nil, err
		}
	case "proxy":
		store, err = builder()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unrecognized storage type %q", storageType)
	}

	if err := store.Init(ctx); err != nil {
		store.Close()
		return nil, err
	}

	return store, nil
}

func loadConfigWithOverrides(opts *Opts) (*types.ApplicationConfig, error) {
	return config.LoadWithOverrides(opts.ConfigFile, opts.OverrideConfig)
}

func relPathFromRoot(file string, env *types.Environment) string {
	if file == "" {
		return ""
	}

	var fullPath string
	if filepath.IsAbs(file) {
		fullPath = file
	} else {
		fullPath = filepath.Join(env.CWD, file)
	}
	rel, err := filepath.Rel(env.GitRoot, fullPath)
	if err != nil {
		return ""
	}
	return rel
}

func applyContextOverrides(appConfig *types.ApplicationConfig, queryConfig *types.QueryConfig) (*types.ApplicationConfig, *types.QueryConfig, error) {
	env, err := environment.New(appConfig.WorkDir, appConfig.Projects)
	if err != nil {
		return nil, nil, err
	}

	var newAppConfig *types.ApplicationConfig
	if env.Project != "" {
		newAppConfig = &types.ApplicationConfig{}
		*newAppConfig = *appConfig
		newAppConfig.Projects = []types.NormalizedProjectConfig{{Name: env.Project}}
	} else {
		newAppConfig = appConfig
	}

	newQueryConfig := *queryConfig
	newQueryConfig.File = relPathFromRoot(queryConfig.File, env)
	newQueryConfig.BeforeCommit = env.HEAD
	newQueryConfig.ApplicationConfig = newAppConfig

	return newAppConfig, &newQueryConfig, nil
}

func gitClientCreator(path string, maxWorkers int) handlers.GitClient {
	return &git.Client{
		Path:       path,
		MaxWorkers: maxWorkers,
		Executor:   &exec.Client{},
	}
}

func dataSink() handlers.DataSinker {
	var sink handlers.DataSinker

	if isatty.IsTerminal(os.Stdout.Fd()) {
		sink = &datasinks.FlatWriter{Writer: os.Stdout, Formatter: diff.NewDecoratedFormatter()}
	} else {
		sink = &handlers.JSONWriter{Writer: os.Stdout}
	}

	return sink
}
