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

// Package commits provides methods for finding commits of interest, such as
// first parent commits and merge bases.
package commits

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/cespare/xxhash/v2"
)

func maybeCachePut(cache Cache, key string, value any) error {
	// Not viewing a nil cache as an error.  It's just a loss
	// of performance.
	if cache == nil {
		return nil
	}
	return cache.Put(key, value)
}

func maybeCacheGet(cache Cache, key string, out any) error {
	// Treat a nil cache as a miss.
	if cache == nil {
		return errors.New("nil cache")
	}
	return cache.Get(key, out)
}

func uniqueFirstParentCommitsHash(ref string, excludeRefs []string, gitClient GitClient) (uint64, error) {
	hash := xxhash.New()
	sha, err := gitClient.RefToSha(ref)
	if err != nil {
		return 0, err
	}
	hash.WriteString(sha)

	sort.Strings(excludeRefs)

	for _, e := range excludeRefs {
		sha, err := gitClient.RefToSha(e)
		if err != nil {
			return 0, err
		}
		hash.WriteString(sha)
	}
	return hash.Sum64(), nil
}

// UniqueFirstParentCommitsOptions are options for `UniqueFirstParentCommits`
type UniqueFirstParentCommitsOptions struct {
	Ref         string
	ExcludeRefs []string
	Cache       Cache
	GitClient   GitClient
}

// UniqueFirstParentCommits retrieves gitClient.UniqueFirstParentCommits
// result from cache if possible, otherwise executes the call.
func UniqueFirstParentCommits(opts *UniqueFirstParentCommitsOptions) ([]string, error) {
	if opts.GitClient == nil {
		return nil, errors.New("GitClient is nil")
	}

	hash, err := uniqueFirstParentCommitsHash(opts.Ref, opts.ExcludeRefs, opts.GitClient)
	if err != nil {
		return nil, err
	}
	key := strconv.FormatUint(hash, 10) + "_UniqueFirstParentCommits"

	var uniqueFirstParentCommits []string
	if err := maybeCacheGet(opts.Cache, key, &uniqueFirstParentCommits); err == nil {
		return uniqueFirstParentCommits, nil
	}

	uniqueFirstParentCommits, err = opts.GitClient.UniqueFirstParentCommits(opts.Ref, opts.ExcludeRefs)
	if err != nil {
		return nil, err
	}
	if err := maybeCachePut(opts.Cache, key, &uniqueFirstParentCommits); err != nil {
		return nil, err
	}

	return uniqueFirstParentCommits, nil
}

func mergeBasesHash(uniqueFirstParentCommits []string, upstreamRef string, gitClient GitClient) (uint64, error) {
	hash := xxhash.New()

	for _, c := range uniqueFirstParentCommits {
		sha, err := gitClient.RefToSha(c)
		if err != nil {
			return 0, err
		}
		hash.WriteString(sha)
	}

	sha, err := gitClient.RefToSha(upstreamRef)
	if err != nil {
		return 0, err
	}
	hash.WriteString(sha)

	return hash.Sum64(), nil
}

// MergeBasesOptions are options for `MergeBases`
type MergeBasesOptions struct {
	UniqueFirstParentCommits []string
	UpstreamRef              string
	MaxWorkers               int
	Cache                    Cache
	GitClient                GitClient
}

// MergeBases returns the merge bases for all commits in
// `uniqueFirstParentCommits` and `upstream`. If a merge base cannot be found,
// an empty string is inserted. Result is returned from cache if found.
func MergeBases(ctx context.Context, opts *MergeBasesOptions) ([]string, error) {
	if opts.GitClient == nil {
		return nil, errors.New("GitClient is nil")
	}

	hash, err := mergeBasesHash(opts.UniqueFirstParentCommits, opts.UpstreamRef, opts.GitClient)
	if err != nil {
		return nil, err
	}
	key := strconv.FormatUint(hash, 10) + "_MergeBases"

	var mergeBases []string
	if err := maybeCacheGet(opts.Cache, key, &mergeBases); err == nil { // If NO error.
		return mergeBases, nil
	}

	numWorkers := max(1, min(opts.MaxWorkers, len(opts.UniqueFirstParentCommits)))
	mergeBases = make([]string, len(opts.UniqueFirstParentCommits))

	type token struct{}
	sem := make(chan token, numWorkers)
	for idx, commit := range opts.UniqueFirstParentCommits {
		sem <- token{}
		go func(i int, c string) {
			// Intentional error ignore:
			// Returning empty string in the event a merge base cannot be found.
			mergeBases[i], _ = opts.GitClient.BestMergeBase(c, opts.UpstreamRef)
			<-sem
		}(idx, commit)
	}

	for range numWorkers {
		sem <- token{}
	}
	close(sem)

	err = maybeCachePut(opts.Cache, key, &mergeBases)
	if err != nil {
		return nil, err
	}

	return mergeBases, err
}
