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
	"path/filepath"

	"log"
	"golang.org/x/sync/errgroup"
	"github.com/schollz/progressbar/v3"
	"chisel/constants"
	"chisel/stats"
	"chisel/storage"
	"chisel/types"
)

// Stats will collect the stat diffs for the input projects and add to storage.
func Stats(ctx context.Context, opts *StatsOptions) error {
	if opts.GitClientCreator == nil {
		return errors.New("GitClientCreator is nil")
	}

	if opts.StatsDifferCreator == nil {
		return errors.New("StatsDifferCreator is nil")
	}

	numProjects := len(opts.ApplicationConfig.Projects)
	numProjectWorkers := max(1, min(opts.ApplicationConfig.MaxProjectWorkers, numProjects))
	numWorkersPerProject := max(1, opts.ApplicationConfig.MaxWorkersPerProject)
	log.Printf("numProjects: %d, numProjectWorkers: %d, numWorkersPerProject: %d", numProjects, numProjectWorkers, numWorkersPerProject)

	eg := new(errgroup.Group)
	eg.SetLimit(numProjectWorkers)

	progressChan := make(chan bool)
	defer close(progressChan)
	progressBar := progressbar.Default(int64(len(opts.ApplicationConfig.Projects)))

	go func() {
		for range progressChan {
			progressBar.Add(1)
		}
		progressBar.Finish()
	}()

	for _, project := range opts.ApplicationConfig.Projects {
		projectName := project.Name
		projectRelPath := project.Path()

		eg.Go(func() error {
			routineTimer := newTimer()
			defer func() {
				progressChan <- true
				log.Printf("Done analyzing project projectName: %q, projectRelPath: %q, duration: %s", projectName, projectRelPath, routineTimer.duration())
			}()

			log.Printf("Analyzing project projectName: %q, projectRelPath: %q", projectName, projectRelPath)
			fullPath := filepath.Join(opts.ApplicationConfig.WorkDir, constants.ReposDir, projectRelPath)
			gitClient := opts.GitClientCreator(fullPath, numWorkersPerProject)
			projectConfig := projectConfigForWorker(opts.ApplicationConfig, projectName, fullPath)

			// Not all projects have the desired upstream branch. Skip analyzing
			// before issuing any storage queries.
			if _, err := gitClient.RefToSha(projectConfig.UpstreamRef()); err != nil {
				log.Printf("Skipping project %v due to missing upstream %q", projectConfig, projectConfig.UpstreamRef())
				return nil
			}

			descriptor, err := opts.Storage.SplitTransactions(ctx, projectConfig, stats.AddStatsAction)
			if err != nil {
				return fmt.Errorf("storage.SplitTransactions failed for project: %q : %v", projectName, err)
			}
			if err := retryAddStats(ctx, opts, projectConfig, descriptor); err != nil {
				return fmt.Errorf("retryaddStats failed for project: %q : %v. No further Analyze runs can proceed", projectName, err)
			}

			downstreamSha, err := gitClient.RefToSha(projectConfig.DownstreamRef())
			if err != nil {
				return err
			}

			done := false
			for !done {
				hOpts := &handleStatOpts{
					projectConfig:        projectConfig,
					downstreamSha:        downstreamSha,
					gitClient:            gitClient,
					numWorkersPerProject: numWorkersPerProject,
					StatsOptions:         opts,
				}
				done, err = handleProjectStats(ctx, hOpts)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	return eg.Wait()
}

func projectConfigForWorker(appConfig *types.ApplicationConfig, name, path string) *types.ProjectConfig {
	return &types.ProjectConfig{
		DownstreamHost:   appConfig.DownstreamHost,
		DownstreamName:   appConfig.DownstreamName,
		DownstreamBranch: appConfig.DownstreamBranch,
		DownstreamRemote: appConfig.DownstreamRemote,
		UpstreamHost:     appConfig.UpstreamHost,
		UpstreamName:     appConfig.UpstreamName,
		UpstreamBranch:   appConfig.UpstreamBranch,
		UpstreamRemote:   appConfig.UpstreamRemote,
		Name:             name,
		Path:             path,
	}
}

type handleStatOpts struct {
	projectConfig        *types.ProjectConfig
	downstreamSha        string
	gitClient            GitClient
	numWorkersPerProject int
	*StatsOptions
}

func handleProjectStats(ctx context.Context, opts *handleStatOpts) (done bool, err error) {
	timer := newTimer()
	projectConfig := opts.projectConfig
	projectName := projectConfig.Name

	log.Printf("Calling storage.MostRecentStatsCommit for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	projectConfig.Since, err = opts.Storage.MostRecentStatsCommit(ctx, projectConfig)
	log.Printf("storage.MostRecentStatsCommit returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		return false, fmt.Errorf("storage.MostRecentStatsCommit failed for project: %q : %v", projectName, err)
	}
	if projectConfig.Since.Commit == "" {
		projectConfig.Since.Commit = constants.BootstrapValue
	}

	if opts.downstreamSha == projectConfig.Since.Commit {
		log.Printf("There aren't any new commits for project %q.", projectConfig.Name)
		return true, nil
	}

	log.Printf("Calling storage.MostRecentFiles for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	mostRecentFiles, err := opts.Storage.MostRecentFiles(ctx, projectConfig)
	log.Printf("storage.MostRecentFiles returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		return false, fmt.Errorf("storage.MostRecentFiles failed for project: %q : %v", projectName, err)
	}

	log.Printf("Calling storage.CurrCommits for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	currCommits, err := opts.Storage.CurrCommits(ctx, projectConfig)
	log.Printf("storage.CurrCommits returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		return false, fmt.Errorf("storage.CurrCommits failed for project: %q :  %v", projectName, err)
	}

	differOpts := &stats.DifferOpts{
		ProjectConfig:         projectConfig,
		Cache:                 opts.Cache,
		GitClient:             opts.gitClient,
		MostRecentBranchFiles: mostRecentFiles,
		SkipCommits:           currCommits,
		EmailMapper:           opts.EmailMapper,
		MaxWorkers:            opts.numWorkersPerProject,
		MaxCommits:            opts.ApplicationConfig.MaxCommitsPerProject,
	}
	differ, err := opts.StatsDifferCreator(differOpts)
	if err != nil {
		log.Printf("Unable to create Differ for project: %q : %v", projectName, err)
		return false, fmt.Errorf("unable to create Differ for project: %q : %v", projectName, err)
	}

	log.Printf("Calling differ.Analyze for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	statObjects, err := differ.Analyze(ctx)
	log.Printf("differ.Analyze returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		log.Printf("Unable to analyze stats for project: %q : %v", projectName, err)
		return false, fmt.Errorf("unable to analyze stats for project: %q : %v", projectName, err)
	}
	done = (statObjects == nil) || statObjects.AllCommitsConsumed

	log.Printf("Calling storage.AddStats for projectName: %q", projectName)
	timer.reset()
	err = opts.Storage.AddStats(ctx, projectConfig, statObjects, DefaultStatsOptions())
	log.Printf("storage.AddStats returned for projectName: %q duration: %s", projectName, timer.duration())
	if err != nil {
		log.Printf("Unable to add stats for project: %q : %v", projectName, err)
		return false, fmt.Errorf("unable to add stats for project: %q : %v", projectName, err)
	}

	return done, nil
}

func retryAddStats(ctx context.Context, opts *StatsOptions, projectConfig *types.ProjectConfig, descriptor *types.ResultsDescriptor) error {
	if descriptor == nil {
		return nil
	}
	if opts.WriteAheadCache == nil {
		return errors.New("write ahead cache is nil")
	}

	var statObjects *stats.StatObjects
	err := opts.WriteAheadCache.Get(stats.KeyForProjectStats(projectConfig), &statObjects)
	if err != nil {
		return fmt.Errorf("could not find required key in write ahead cache: %v", err)
	}

	checkSum, err := stats.Checksum(statObjects)
	if err != nil {
		return err
	}
	if checkSum != descriptor.Checksum {
		return fmt.Errorf("checksums differ got: %d expecting: %d", checkSum, descriptor.Checksum)
	}

	return opts.Storage.AddStats(ctx, projectConfig, statObjects, &storage.AddStatsOptions{IsRetry: true})
}
