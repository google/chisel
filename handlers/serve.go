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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"chisel/constants"
	"chisel/contents"
	"chisel/storage"
	"chisel/types"
)

// WriteAsJSON writes input data as a slice of bytes to the provided writer.
func WriteAsJSON(writer io.Writer, data any) error {
	b, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = writer.Write(b)
	return err
}

// JSONWriter implements the DataSinker interface and formats the data
// as marshalled JSON.
type JSONWriter struct {
	Writer io.Writer
}

// Commit is a datasink for the `list commit` use case.
func (j *JSONWriter) Commit(commit *contents.CommitListItem) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, commit)
}

// Commits is a datasink for the `list commits` use case.
func (j *JSONWriter) Commits(commits []*contents.CommitListItem) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, commits)
}

// Files is a datasink for the `list files` use case.
func (j *JSONWriter) Files(files []string) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, files)
}

// Hunk is a datasink for the `list hunk` use case.
func (j *JSONWriter) Hunk(hunk *contents.HunkListItem) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, hunk)
}

// Hunks is a datasink for the `list hunks` use case.
func (j *JSONWriter) Hunks(hunks []*contents.HunkListItem) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, hunks)
}

// DAG is a datasink for the `list dag` use case.
func (j *JSONWriter) DAG(hunks []*contents.HunkListItem) error {
	return j.Hunks(hunks)
}

// LatestCommit is a datasink for the `list latest_*_commit` use case.
func (j *JSONWriter) LatestCommit(commit string) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, commit)
}

// LatestCommits is a datasink for the `list latest_*_commits` use case.
func (j *JSONWriter) LatestCommits(commits []types.ProjectCommit) error {
	if j.Writer == nil {
		return fmt.Errorf("nil Writer")
	}

	return WriteAsJSON(j.Writer, commits)
}

func handlerFunc(ctx context.Context, appConfig *types.ApplicationConfig, storage storage.Storage, action string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rc, err := requestConfig(r.URL.Query(), appConfig)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var buffer bytes.Buffer
		if err := List(ctx, action, rc, storage, &JSONWriter{&buffer}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Write(buffer.Bytes())
	}
}

func createServeMux(ctx context.Context, appConfig *types.ApplicationConfig, storage storage.Storage) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/"+constants.CommitEndpoint, handlerFunc(ctx, appConfig, storage, constants.CommitAction))
	mux.HandleFunc("/"+constants.CommitsEndpoint, handlerFunc(ctx, appConfig, storage, constants.CommitsAction))
	mux.HandleFunc("/"+constants.HunkEndpoint, handlerFunc(ctx, appConfig, storage, constants.HunkAction))
	mux.HandleFunc("/"+constants.HunksEndpoint, handlerFunc(ctx, appConfig, storage, constants.HunksAction))
	mux.HandleFunc("/"+constants.FilesEndpoint, handlerFunc(ctx, appConfig, storage, constants.FilesAction))

	return mux
}

// Serve behaves as a proxy to storage to reuse an existing database connection.
func Serve(ctx context.Context, appConfig *types.ApplicationConfig, storage storage.Storage) error {
	httpEndpoint := fmt.Sprintf("localhost:%d", appConfig.ProxyPort)

	fmt.Printf("Starting server on %s\n", httpEndpoint)
	if err := http.ListenAndServe(httpEndpoint, createServeMux(ctx, appConfig, storage)); err != nil {
		return err
	}

	return nil
}

func requestConfig(values url.Values, initialConfig *types.ApplicationConfig) (*types.QueryConfig, error) {
	requestAppConfig := *initialConfig
	requestAppConfig.DownstreamName = values.Get(constants.DownstreamHostNameParam)
	requestAppConfig.DownstreamBranch = values.Get(constants.DownstreamBranchParam)
	requestAppConfig.UpstreamName = values.Get(constants.UpstreamHostNameParam)
	requestAppConfig.UpstreamBranch = values.Get(constants.UpstreamBranchParam)
	requestAppConfig.Projects = []types.NormalizedProjectConfig{
		{
			Name: values.Get(constants.ProjectParam),
		},
	}

	requestConfig := &types.QueryConfig{
		ApplicationConfig: &requestAppConfig,
	}
	requestConfig.Commit = values.Get(constants.CommitParam)
	requestConfig.BeforeCommit = values.Get(constants.BeforeCommitParam)
	requestConfig.File = values.Get(constants.FileParam)
	requestConfig.HunkID = values.Get(constants.HunkIDParam)
	line := values.Get(constants.LineParam)
	if line != "" {
		l64, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return nil, errors.New("could not parse Line parameter")
		}
		requestConfig.Line = int(l64)
	}

	return requestConfig, nil
}
