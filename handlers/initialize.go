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

package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"log"
	"github.com/google/safeopen"
	"golang.org/x/sync/errgroup"
	"chisel/config"
	"chisel/constants"
	"chisel/types"
)

func setupProjectDirs(ctx context.Context, opts *InitializeOptions) error {
	eg := new(errgroup.Group)
	eg.SetLimit(runtime.NumCPU())

	config := opts.ApplicationConfig
	sameRemotes := config.UpstreamRemote == config.DownstreamRemote
	sameHosts := config.UpstreamHost == config.DownstreamHost
	if sameRemotes && !sameHosts {
		return fmt.Errorf("the upstream remote host %q will clobber the downstream remote host %q", config.UpstreamHost, config.DownstreamHost)
	}

	var (
		remoteNames      []string
		mustFetchTargets []string
	)
	if config.DownstreamRemote != "" {
		remoteNames = append(remoteNames, config.DownstreamRemote)
		mustFetchTargets = append(mustFetchTargets, types.BranchRef(config.DownstreamRemote, config.DownstreamBranch))
	}
	if config.UpstreamRemote != "" {
		if !sameRemotes {
			remoteNames = append(remoteNames, config.UpstreamRemote)
		}
		mustFetchTargets = append(mustFetchTargets, types.BranchRef(config.UpstreamRemote, config.UpstreamBranch))
	}

	for _, project := range config.Projects {
		name := project.Name
		path := filepath.Join(config.WorkDir, constants.ReposDir, project.Path())
		gitClient := opts.GitClientCreator(path, 1)

		eg.Go(func() error {
			if err := os.MkdirAll(path, 0750); err != nil {
				log.Printf("Unable to make directory %q : %v", path, err)
				return err
			}
			var remoteURLs []string
			if config.DownstreamHost != "" {
				remoteURLs = append(remoteURLs, config.DownstreamHost+"/"+name)
			}
			if config.UpstreamHost != "" && !sameRemotes {
				remoteURLs = append(remoteURLs, config.UpstreamHost+"/"+name)
			}

			if err := gitClient.InitRepo(remoteNames, remoteURLs); err != nil {
				return err
			}

			if err := gitClient.FetchBranches(mustFetchTargets, nil); err != nil {
				// Not all projects have desired fetch targets and a warning is
				// sufficient here. Don't propagate up the error.
				log.Printf("Unable to fetch branches for project: %q : %v", project, err)
			}

			return nil
		})
	}

	return eg.Wait()
}

// Initialize initializes and syncs source git repos.
func Initialize(ctx context.Context, opts *InitializeOptions) error {
	workDir := opts.ApplicationConfig.WorkDir
	if workDir == "" {
		return errors.New("work directory is empty")
	}
	if err := os.MkdirAll(workDir, 0750); err != nil {
		return fmt.Errorf("unable to make directory %q : %v", workDir, err)
	}

	f, err := safeopen.OpenFileBeneath(workDir, constants.DefaultConfigFile, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := config.WriteConfig(opts.ApplicationConfig, f); err != nil {
		return err
	}
	return setupProjectDirs(ctx, opts)
}
