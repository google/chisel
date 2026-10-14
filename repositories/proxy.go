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

package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"chisel/constants"
	"chisel/contents"
	"chisel/stats"
	"chisel/storage"
	"chisel/types"
)

func unsupportedError(method string) error {
	return fmt.Errorf("unsupported command %q for Proxy", method)
}

// Proxy implements a subset of the chisel.Storage interface and issues GET
// requests to a server running on localhost. The motivation for its use is
// to take advantage of a single persistent connection to persistent storage
// running on the server.
type Proxy struct {
}

func init() {
	builder := func(data ...any) (storage.Storage, error) {
		return &Proxy{}, nil
	}
	storage.Register("proxy", builder)
}

// Init is an empty method for Proxy.
func (p *Proxy) Init(ctx context.Context) error {
	return nil
}

// MostRecentStatsCommits is an empty method for Proxy.
func (p *Proxy) MostRecentStatsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error) {
	return nil, unsupportedError("MostRecentStatsCommits")
}

// MostRecentStatsCommit is an empty method for Proxy.
func (p *Proxy) MostRecentStatsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error) {
	return types.SinceCommit{}, unsupportedError("MostRecentStatsCommit")
}

// MostRecentFiles is an empty method for Proxy.
func (p *Proxy) MostRecentFiles(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]*stats.BranchFile, error) {
	return nil, unsupportedError("MostRecentFiles")
}

// CurrCommits is an empty method for Proxy.
func (p *Proxy) CurrCommits(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]bool, error) {
	return nil, unsupportedError("CurrCommits")
}

// AddStats is an empty method for Proxy.
func (p *Proxy) AddStats(ctx context.Context, projectConfig *types.ProjectConfig, statObjects *stats.StatObjects, opts *storage.AddStatsOptions) error {
	return unsupportedError("AddStats")
}

// AllHashes is an empty method for Proxy.
func (p *Proxy) AllHashes(ctx context.Context) (map[int64]bool, error) {
	return nil, unsupportedError("AllHashes")
}

// MostRecentContentsCommits is an empty method for Proxy.
func (p *Proxy) MostRecentContentsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error) {
	return nil, unsupportedError("MostRecentContentsCommits")
}

// MostRecentContentsCommit is an empty method for Proxy.
func (p *Proxy) MostRecentContentsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error) {
	return types.SinceCommit{}, unsupportedError("MostRecentContentsCommit")
}

// MostRecentLiveHunks is an empty method for Proxy.
func (p *Proxy) MostRecentLiveHunks(ctx context.Context, projectConfig *types.ProjectConfig) (contents.LiveHunkSegmentsData, error) {
	return nil, unsupportedError("MostRecentLiveHunks")
}

// MostRecentDeadHunks is an empty method for Proxy.
func (p *Proxy) MostRecentDeadHunks(ctx context.Context, projectConfig *types.ProjectConfig) (contents.DeadHunkSegmentsData, error) {
	return nil, unsupportedError("MostRecentDeadHunks")
}

// AddContentHashes is an empty method for Proxy.
func (p *Proxy) AddContentHashes(ctx context.Context, hashToContent map[int64][]string) error {
	return unsupportedError("AddContentHashes")
}

// SplitTransactions is an empty method for Proxy.
func (p *Proxy) SplitTransactions(ctx context.Context, projectConfig *types.ProjectConfig, action string) (*types.ResultsDescriptor, error) {
	return nil, unsupportedError("SplitTransactions")
}

// AddDiffResults is an empty method for Proxy.
func (p *Proxy) AddDiffResults(ctx context.Context, projectConfig *types.ProjectConfig, diffResults *contents.DiffResults, options *storage.AddDiffResultsOptions) error {
	return unsupportedError("AddDiffResults")
}

// ContentForHash is an empty method for Proxy.
func (p *Proxy) ContentForHash(ctx context.Context, hash int64) (string, error) {
	return "", unsupportedError("ContentForHash")
}

// HunkByID returns the hunk matching the ID.
func (p *Proxy) HunkByID(ctx context.Context, queryConfig *types.QueryConfig) (*contents.HunkListItem, error) {
	body, err := get(constants.HunkEndpoint, queryConfig)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var data *contents.HunkListItem

	err = json.NewDecoder(body).Decode(&data)
	return data, err
}

// SortedFiles returns all files present in storage.
// Files for all commits are returned unless applicationConfig.Commit is
// a non-empty string.
func (p *Proxy) SortedFiles(ctx context.Context, queryConfig *types.QueryConfig) ([]string, error) {
	body, err := get(constants.FilesEndpoint, queryConfig)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var data []string

	err = json.NewDecoder(body).Decode(&data)
	return data, err
}

// Commit returns the commit from storage.
func (p *Proxy) Commit(ctx context.Context, queryConfig *types.QueryConfig) (*contents.CommitListItem, error) {
	body, err := get(constants.CommitEndpoint, queryConfig)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var data *contents.CommitListItem

	err = json.NewDecoder(body).Decode(&data)
	return data, err
}

// SortedCommits returns all commits present in storage.
func (p *Proxy) SortedCommits(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.CommitListItem, error) {
	body, err := get(constants.CommitsEndpoint, queryConfig)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var data []*contents.CommitListItem

	err = json.NewDecoder(body).Decode(&data)
	return data, err
}

// SortedHunksForFile returns all Hunk Segments for a file.
// Hunks for all commits are returned unless appConfig.Commit is
// a non-empty string.
func (p *Proxy) SortedHunksForFile(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.HunkListItem, error) {
	body, err := get(constants.HunksEndpoint, queryConfig)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var data []*contents.HunkListItem

	err = json.NewDecoder(body).Decode(&data)
	return data, err
}

// Close is an empty method for Proxy.
func (p *Proxy) Close() error {
	return nil
}

func produceURL(endpoint string, queryConfig *types.QueryConfig) string {
	appConfig := queryConfig.ApplicationConfig

	params := url.Values{}
	params.Add(constants.DownstreamHostNameParam, appConfig.DownstreamName)
	params.Add(constants.DownstreamBranchParam, appConfig.DownstreamBranch)
	params.Add(constants.UpstreamHostNameParam, appConfig.UpstreamName)
	params.Add(constants.UpstreamBranchParam, appConfig.UpstreamBranch)
	project := ""
	if len(appConfig.Projects) > 0 {
		project = appConfig.Projects[0].Name
	}
	params.Add(constants.ProjectParam, project)
	params.Add(constants.BeforeCommitParam, queryConfig.BeforeCommit)
	params.Add(constants.CommitParam, queryConfig.Commit)
	params.Add(constants.FileParam, queryConfig.File)
	params.Add(constants.HunkIDParam, queryConfig.HunkID)
	params.Add(constants.LineParam, fmt.Sprintf("%d", queryConfig.Line))

	return fmt.Sprintf("http://localhost:%d/%s?%s", appConfig.ProxyPort, endpoint, params.Encode())
}

func get(endpoint string, queryConfig *types.QueryConfig) (io.ReadCloser, error) {
	url := produceURL(endpoint, queryConfig)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusBadRequest {
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		return nil, errors.New(string(body))
	}

	return resp.Body, nil
}
