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
	"chisel/contents"
	"chisel/storage"
	"chisel/types"
)

func retryAddDiffResults(ctx context.Context, opts *ContentsOptions, projectConfig *types.ProjectConfig, descriptor *types.ResultsDescriptor) error {
	if descriptor == nil {
		return nil
	}
	if opts.WriteAheadCache == nil {
		return errors.New("write ahead cache is nil")
	}

	var diffResults *contents.DiffResults
	err := opts.WriteAheadCache.Get(contents.KeyForProjectDiff(projectConfig), &diffResults)
	if err != nil {
		return fmt.Errorf("could not find required key in write ahead cache: %v", err)
	}

	checkSum, err := contents.Checksum(diffResults)
	if err != nil {
		return err
	}
	if checkSum != descriptor.Checksum {
		return fmt.Errorf("checksums differ got: %d expecting: %d", checkSum, descriptor.Checksum)
	}

	return opts.Storage.AddDiffResults(ctx, projectConfig, diffResults, &storage.AddDiffResultsOptions{IsRetry: true})
}

// Contents will collect the content diffs for the input projects and add to storage.
func Contents(ctx context.Context, opts *ContentsOptions) error {
	if opts.GitClientCreator == nil {
		return errors.New("GitClientCreator is nil")
	}

	if opts.ContentsDifferCreator == nil {
		return errors.New("ContentsDifferCreator is nil")
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

	a, err := opts.Storage.AllHashes(ctx)
	if err != nil {
		return fmt.Errorf("storage.AllHashes: %v", err)

	}
	allHashes := &AllHashes{
		h: a,
	}

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

			descriptor, err := opts.Storage.SplitTransactions(ctx, projectConfig, contents.DiffResultsAction)
			if err != nil {
				return fmt.Errorf("storage.SplitTransactions failed for project: %q : %v", projectName, err)
			}
			if err := retryAddDiffResults(ctx, opts, projectConfig, descriptor); err != nil {
				return fmt.Errorf("retryAddDiffResults failed for project: %q : %v. No further Analyze runs can proceed", projectName, err)
			}

			downstreamSha, err := gitClient.RefToSha(projectConfig.DownstreamRef())
			if err != nil {
				return err
			}

			done := false
			for !done {
				hOpts := &handleContentsOpts{
					projectConfig:        projectConfig,
					downstreamSha:        downstreamSha,
					gitClient:            gitClient,
					numWorkersPerProject: numWorkersPerProject,
					allHashes:            allHashes,
					ContentsOptions:      opts,
				}
				done, err = handleProjectContents(ctx, hOpts)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	return eg.Wait()
}

type handleContentsOpts struct {
	projectConfig        *types.ProjectConfig
	downstreamSha        string
	gitClient            GitClient
	numWorkersPerProject int
	allHashes            *AllHashes
	*ContentsOptions
}

func handleProjectContents(ctx context.Context, opts *handleContentsOpts) (done bool, err error) {
	timer := newTimer()
	projectConfig := opts.projectConfig
	projectName := projectConfig.Name

	log.Printf("Calling storage.MostRecentContentsCommit for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	projectConfig.Since, err = opts.Storage.MostRecentContentsCommit(ctx, projectConfig)
	log.Printf("storage.MostRecentContentsCommit returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		return false, fmt.Errorf("storage.MostRecentContentsCommit failed for project: %q : %v", projectName, err)
	}
	if projectConfig.Since.Commit == "" {
		projectConfig.Since.Commit = constants.BootstrapValue
	}

	if opts.downstreamSha == projectConfig.Since.Commit {
		log.Printf("There aren't any new commits for project %q.", projectConfig.Name)
		return true, nil
	}

	log.Printf("Calling storage.MostRecentLiveHunks for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	mostRecentLiveHunks, err := opts.Storage.MostRecentLiveHunks(ctx, projectConfig)
	log.Printf("storage.MostRecentLiveHunks returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		return false, fmt.Errorf("storage.MostRecentLiveHunks failed for project: %q : %v", projectName, err)
	}

	differOpts := &contents.DifferOpts{
		ProjectConfig:       projectConfig,
		Cache:               opts.Cache,
		GitClient:           opts.gitClient,
		MaxWorkers:          opts.numWorkersPerProject,
		MaxCommits:          opts.ApplicationConfig.MaxCommitsPerProject,
		MostRecentLiveHunks: mostRecentLiveHunks,
		Files:               opts.Files,
	}
	differ, err := opts.ContentsDifferCreator(differOpts)
	if err != nil {
		log.Printf("Unable to create Differ for project: %q : %v", projectName, err)
		return false, fmt.Errorf("unable to create Differ for project: %q : %v", projectName, err)
	}

	log.Printf("Calling differ.Analyze for projectName: %q with Since.Commit: %q", projectName, projectConfig.Since.Commit)
	timer.reset()
	diffResults, err := differ.Analyze(ctx)
	log.Printf("differ.Analyze returned for projectName: %q with Since.Commit: %q duration: %s", projectName, projectConfig.Since.Commit, timer.duration())
	if err != nil {
		log.Printf("Unable to analyze contents for project: %q : %v", projectName, err)
		return false, fmt.Errorf("unable to analyze contents for project: %q : %v", projectName, err)
	}
	done = (diffResults == nil) || diffResults.AllCommitsConsumed

	addContentHashes := func() error {
		if diffResults == nil {
			return nil
		}
		// The content hashes are not project specific, but they need to be present
		// in storage prior to the related diff results to avoid primary key
		// reference issues. Therefore, updates to diffResults as a result of
		// allHashes states must be pushed to storage as an atomic unit.
		opts.allHashes.mutex.Lock()
		defer opts.allHashes.mutex.Unlock()
		for hash := range diffResults.HashToContent {
			if opts.allHashes.h[hash] {
				delete(diffResults.HashToContent, hash)
			}
		}

		log.Printf("Calling storage.AddContentHashes for projectName: %q", projectName)
		timer.reset()
		err := opts.Storage.AddContentHashes(ctx, diffResults.HashToContent)
		log.Printf("storage.AddContentHashes returned for projectName: %q duration: %s", projectName, timer.duration())
		if err != nil {
			log.Printf("storage.AddContentHashes failed for project: %q : %v", projectName, err)
			return fmt.Errorf("storage.AddContentHashes failed for project: %q : %v", projectName, err)
		}

		// Only update the in-memory representation of allHashes after they
		// are successfully persisted to storage.
		for hash := range diffResults.HashToContent {
			opts.allHashes.h[hash] = true
		}

		return nil
	}
	if err := addContentHashes(); err != nil {
		return false, err
	}

	log.Printf("Calling storage.AddDiffResults for projectName: %q", projectName)
	timer.reset()
	err = opts.Storage.AddDiffResults(ctx, projectConfig, diffResults, DefaultDiffResultOptions())
	log.Printf("storage.AddDiffResults returned for projectName: %q duration: %s", projectName, timer.duration())
	if err != nil {
		log.Printf("storage.AddDiffResults failed for project: %q : %v", projectName, err)
		return false, fmt.Errorf("storage.AddDiffResults failed for project: %q : %v", projectName, err)
	}

	return done, nil
}
