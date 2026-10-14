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

// Package mcp implements the Chisel MCP server.
package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"chisel/contents"
	"chisel/storage"
	"chisel/types"
)

// Storage defines the interface for the Storage providing MCP API.
type Storage interface {
	// DownstreamUpstreamPairings returns all supported downstream/upstream pairings found in the store.
	DownstreamUpstreamPairings(ctx context.Context) ([]storage.DownstreamUpstreamPairing, error)

	// MostRecentStatsCommits returns the most recent analyzed commit for all projects.
	MostRecentStatsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error)

	// MostRecentStatsCommit returns the most recent analyzed commit and metadata for a single project.
	MostRecentStatsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error)

	// MostRecentProjectDeltas returns the project level deltas for the most recently analyzed commit.
	// If projectConfig.Name is empty, all projects are returned. Otherwise, only the provided
	// project is returned.
	MostRecentProjectDeltas(ctx context.Context, projectConfig *types.ProjectConfig) ([]storage.ProjectDelta, error)

	// ProjectTimeSeries shows how a project changes over time.
	ProjectTimeSeries(ctx context.Context, projectConfig *types.ProjectConfig) ([]storage.ProjectTimeSeries, error)

	// MostRecentFileDeltas returns the file deltas for a project at the latest analyzed commit.
	// If path is empty, all files are returned. Otherwise, only the provided
	// file is returned.
	MostRecentFileDeltas(ctx context.Context, projectConfig *types.ProjectConfig, path string, opts *storage.FileDeltasOptions) ([]storage.FileDelta, error)

	// CommitBugs shows the bugs associated with commits.
	CommitBugs(ctx context.Context, projectConfig *types.ProjectConfig) (storage.CommitBugs, error)

	// CommitDeltas returns branch commit level statistics.
	// If commit is empty, all commits are returned. Otherwise, only the provided
	// commit is returned.
	CommitDeltas(ctx context.Context, projectConfig *types.ProjectConfig, commit string, opts *storage.CommitDeltasOptions) ([]storage.CommitDelta, error)

	// CommitFileDeltas returns branch file level statistics.
	CommitFileDeltas(ctx context.Context, projectConfig *types.ProjectConfig, opts *storage.CommitFileDeltasOptions) ([]storage.CommitFileDelta, error)

	// HunksForCommit returns all hunks for a project at a specific commit.
	HunksForCommit(ctx context.Context, projectConfig *types.ProjectConfig, commit string, opts *storage.HunksForCommitOptions) ([]*contents.HunkListItem, error)

	// HunksForFile returns all hunks for a file in a project.
	HunksForFile(ctx context.Context, projectConfig *types.ProjectConfig, path string) ([]*contents.HunkListItem, error)

	// HunkChain returns the chain of hunks starting from a specific hunk ID.
	HunkChain(ctx context.Context, projectConfig *types.ProjectConfig, hunkID string, path string) ([]*contents.HunkListItem, error)

	// Close closes the storage.
	Close() error
}

// StorageFactory is a function that creates a new Storage instance.
type StorageFactory func(ctx context.Context, storageConfig types.StorageConfig) (Storage, error)

// cached storage instance or configuration.
type storageEntry struct {
	mu sync.Mutex

	// Previously created Storage.
	store Storage

	// Config used to lazily create a Storage if store is nil.
	cfg types.StorageConfig
}

// Server implements the MCP API.
// Note that API may appear to return unnecessary data.
// e.g. A request that provides the host receives a response that includes the host.
// However, with LLMs, context is king and self-describing data provides that. It
// is a tradeoff of context vs. tokens. A user may have queries that change the input
// parameters, and without self-describing data, cross-pollination can occur.
// Also, the return types are row-based JSON objects, rather than column-based. Row-based
// is more natural for the LLM to parse but has a heavier payload.
type Server struct {
	storages map[string]*storageEntry
	mu       sync.RWMutex
	factory  StorageFactory
}

// NewServer creates a new Server.
func NewServer(factory StorageFactory) (*Server, error) {
	return &Server{
		storages: make(map[string]*storageEntry),
		factory:  factory,
	}, nil
}

// Close closes all initialized storage instances in the pool.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errorMsgs []string
	for name, entry := range s.storages {
		entry.mu.Lock()
		if entry.store != nil {
			if err := entry.store.Close(); err != nil {
				errorMsgs = append(errorMsgs, fmt.Sprintf("failed to close storage %q: %v", name, err))
			}
			entry.store = nil
		}
		entry.mu.Unlock()
	}
	if len(errorMsgs) > 0 {
		return fmt.Errorf("errors while closing storages:\n%s", strings.Join(errorMsgs, "\n"))
	}
	return nil
}

// getStorage looks up a storage in the pool.
// It supports a default storage (empty string name) if only one storage is registered.
// It will lazily create a storage instance if needed.
func (s *Server) getStorage(ctx context.Context, name string) (Storage, error) {
	var entry *storageEntry
	s.mu.RLock()

	if name == "" {
		if len(s.storages) == 1 {
			for _, e := range s.storages {
				entry = e
				break
			}
		} else if len(s.storages) == 0 {
			s.mu.RUnlock()
			return nil, fmt.Errorf("no storages registered in pool")
		} else {
			s.mu.RUnlock()
			return nil, fmt.Errorf("storage name must be specified when multiple storages are registered")
		}
	} else {
		var ok bool
		entry, ok = s.storages[name]
		if !ok {
			s.mu.RUnlock()
			return nil, fmt.Errorf("storage %q not found in pool", name)
		}
	}
	s.mu.RUnlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Return cached storage if available.
	if entry.store != nil {
		return entry.store, nil
	}

	// Otherwise, we need to create it and save it in the pool entry.
	if s.factory == nil {
		return nil, fmt.Errorf("storage factory not configured")
	}

	store, err := s.factory(ctx, entry.cfg)
	if err != nil {
		return nil, err
	}
	entry.store = store
	return store, nil
}

// AddStorage adds a already created storage to the pool.
func (s *Server) AddStorage(name string, store Storage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storages[name] = &storageEntry{store: store}
}

// AddStorageConfig adds a storage configuration to the pool for lazy initialization.
func (s *Server) AddStorageConfig(name string, config types.StorageConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storages[name] = &storageEntry{cfg: config}
}

// UpstreamDownstream groups an upstream and downstream.
type UpstreamDownstream struct {
	StorageName      string `json:"storageName,omitempty" jsonschema:"Name of the pooled storage"`
	UpstreamName     string `json:"upstreamName" jsonschema:"Name of the upstream host"`
	UpstreamBranch   string `json:"upstreamBranch" jsonschema:"Name of the upstream branch"`
	DownstreamName   string `json:"downstreamName" jsonschema:"Name of the downstream host"`
	DownstreamBranch string `json:"downstreamBranch" jsonschema:"Name of the downstream branch"`
}

// UpstreamDownstreamProject groups an upstream and downstream with a project.
type UpstreamDownstreamProject struct {
	UpstreamDownstream
	Project string `json:"project" jsonschema:"Name of the project"`
}

// UpstreamDownstreamOptionalProject groups an upstream and downstream with an optional project.
type UpstreamDownstreamOptionalProject struct {
	UpstreamDownstream
	Project string `json:"project,omitempty" jsonschema:"Name of the project"`
}

// UpstreamDownstreamProjectOptionalCommit groups an upstream and downstream with a project and optional commit.
type UpstreamDownstreamProjectOptionalCommit struct {
	UpstreamDownstreamProject
	Commit string `json:"commit,omitempty" jsonschema:"Commit SHA"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of results to return"`
	Offset int    `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

// UpstreamDownstreamProjectOptionalPath groups an upstream and downstream with a project and optional file path.
type UpstreamDownstreamProjectOptionalPath struct {
	UpstreamDownstreamProject
	Path   string `json:"path,omitempty" jsonschema:"Path to the file"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of results to return"`
	Offset int    `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

// UpstreamDownstreamProjectCommit groups an upstream and downstream with a project, required commit, and optional pagination.
type UpstreamDownstreamProjectCommit struct {
	UpstreamDownstreamProject
	Commit string `json:"commit" jsonschema:"Commit SHA"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Max number of results to return"`
	Offset int    `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

// UpstreamDownstreamProjectPath groups an upstream and downstream with a project and required file path.
type UpstreamDownstreamProjectPath struct {
	UpstreamDownstreamProject
	Path string `json:"path" jsonschema:"Path to the file"`
}

// UpstreamDownstreamProjectPagination groups an upstream and downstream with a project and pagination options.
type UpstreamDownstreamProjectPagination struct {
	UpstreamDownstreamProject
	Limit  int `json:"limit,omitempty" jsonschema:"Max number of results to return"`
	Offset int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

// DownstreamProject groups an upstream and downstream.
type DownstreamProject struct {
	StorageName    string `json:"storageName,omitempty" jsonschema:"Name of the pooled storage"`
	DownstreamName string `json:"downstreamName" jsonschema:"Name of the downstream host"`
	Project        string `json:"project" jsonschema:"Name of the project"`
}

// StorageOnlyInput is for tools that only need to identify the storage.
type StorageOnlyInput struct {
	StorageName string `json:"storageName,omitempty" jsonschema:"Name of the pooled storage"`
}

// HunkChainInput defines the input for the hunk_chain tool.
type HunkChainInput struct {
	UpstreamDownstreamProject
	HunkID string `json:"hunkId" jsonschema:"ID of the hunk to trace"`
	Path   string `json:"path" jsonschema:"Path to the file"`
}

// DownstreamUpstreamPairings handles the downstream_upstream_pairings tool.
func (s *Server) DownstreamUpstreamPairings(ctx context.Context, req *mcp.CallToolRequest, in StorageOnlyInput) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	pairings, err := store.DownstreamUpstreamPairings(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(pairings) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		Pairings []storage.DownstreamUpstreamPairing
	}
	payload := Payload{
		Pairings: pairings,
	}
	return nil, payload, nil
}

// MostRecentStatsCommits handles the most_recent_stats_commits tool.
func (s *Server) MostRecentStatsCommits(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstream) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	commits, err := store.MostRecentStatsCommits(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(commits) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Commits          []types.ProjectCommit
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		commits,
	}

	return nil, payload, nil
}

// MostRecentStatsCommit handles the most_recent_stats_commit tool.
func (s *Server) MostRecentStatsCommit(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProject) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	commit, err := store.MostRecentStatsCommit(ctx, &types.ProjectConfig{
		Name:             in.Project,
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
	})
	if err != nil {
		return nil, nil, err
	}
	if commit.Commit == "" {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		SinceCommit      types.SinceCommit
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		commit,
	}
	return nil, payload, nil
}

// MostRecentProjectDeltas handles the project_deltas tool.
func (s *Server) MostRecentProjectDeltas(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamOptionalProject) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	deltas, err := store.MostRecentProjectDeltas(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(deltas) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Deltas           []storage.ProjectDelta
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		deltas,
	}
	return nil, payload, nil
}

// ProjectTimeSeries handles the project_time_series tool.
func (s *Server) ProjectTimeSeries(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProject) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	timeSeries, err := store.ProjectTimeSeries(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(timeSeries) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		TimeSeries       []storage.ProjectTimeSeries
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		timeSeries,
	}
	return nil, payload, nil
}

// MostRecentFileDeltas handles the file_deltas tool.
func (s *Server) MostRecentFileDeltas(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProjectOptionalPath) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	fileDeltas, err := store.MostRecentFileDeltas(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, in.Path, &storage.FileDeltasOptions{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return nil, nil, err
	}
	if len(fileDeltas) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		FileDeltas       []storage.FileDelta
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		fileDeltas,
	}
	return nil, payload, nil
}

// CommitBugs handles the commit_bugs tool.
func (s *Server) CommitBugs(ctx context.Context, req *mcp.CallToolRequest, in DownstreamProject) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	commitBugs, err := store.CommitBugs(ctx, &types.ProjectConfig{
		DownstreamName: in.DownstreamName,
		Name:           in.Project,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(commitBugs) == 0 {
		return nil, nil, nil
	}
	return nil, commitBugs, nil
}

// CommitDeltas handles the commit_deltas tool.
func (s *Server) CommitDeltas(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProjectOptionalCommit) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	commitDeltas, err := store.CommitDeltas(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, in.Commit, &storage.CommitDeltasOptions{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return nil, nil, err
	}
	if len(commitDeltas) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		CommitDeltas     []storage.CommitDelta
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		commitDeltas,
	}
	return nil, payload, nil
}

// CommitFileDeltas handles the commit_file_deltas tool.
func (s *Server) CommitFileDeltas(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProjectPagination) (*mcp.CallToolResult, any, error) {
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	commitFileDeltas, err := store.CommitFileDeltas(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, &storage.CommitFileDeltasOptions{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return nil, nil, err
	}
	if len(commitFileDeltas) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		CommitFileDeltas []storage.CommitFileDelta
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		commitFileDeltas,
	}
	return nil, payload, nil
}

// HunksForCommit handles the hunks_for_commit tool.
func (s *Server) HunksForCommit(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProjectCommit) (*mcp.CallToolResult, any, error) {
	if in.Commit == "" {
		return nil, nil, fmt.Errorf("commit is required")
	}
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	hunks, err := store.HunksForCommit(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, in.Commit, &storage.HunksForCommitOptions{Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return nil, nil, err
	}
	if len(hunks) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		Commit           string
		Hunks            []*contents.HunkListItem
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		in.Commit,
		hunks,
	}
	return nil, payload, nil
}

// HunksForFile handles the hunks_for_file tool.
func (s *Server) HunksForFile(ctx context.Context, req *mcp.CallToolRequest, in UpstreamDownstreamProjectPath) (*mcp.CallToolResult, any, error) {
	if in.Path == "" {
		return nil, nil, fmt.Errorf("path is required")
	}
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	hunks, err := store.HunksForFile(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, in.Path)
	if err != nil {
		return nil, nil, err
	}
	if len(hunks) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		Path             string
		Hunks            []*contents.HunkListItem
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		in.Path,
		hunks,
	}
	return nil, payload, nil
}

// HunkChain handles the hunk_chain tool.
func (s *Server) HunkChain(ctx context.Context, req *mcp.CallToolRequest, in HunkChainInput) (*mcp.CallToolResult, any, error) {
	if in.HunkID == "" {
		return nil, nil, fmt.Errorf("hunkId is required")
	}
	store, err := s.getStorage(ctx, in.StorageName)
	if err != nil {
		return nil, nil, err
	}
	if in.Path == "" {
		return nil, nil, fmt.Errorf("path is required")
	}
	hunks, err := store.HunkChain(ctx, &types.ProjectConfig{
		DownstreamName:   in.DownstreamName,
		DownstreamBranch: in.DownstreamBranch,
		UpstreamName:     in.UpstreamName,
		UpstreamBranch:   in.UpstreamBranch,
		Name:             in.Project,
	}, in.HunkID, in.Path)
	if err != nil {
		return nil, nil, err
	}
	if len(hunks) == 0 {
		return nil, nil, nil
	}
	type Payload struct {
		DownstreamName   string
		DownstreamBranch string
		UpstreamName     string
		UpstreamBranch   string
		Project          string
		HunkID           string
		Path             string
		Hunks            []*contents.HunkListItem
	}
	payload := Payload{
		in.DownstreamName,
		in.DownstreamBranch,
		in.UpstreamName,
		in.UpstreamBranch,
		in.Project,
		in.HunkID,
		in.Path,
		hunks,
	}
	return nil, payload, nil
}

// RegisterStorageInput defines the input for registering a storage.
type RegisterStorageInput struct {
	Name            string `json:"name" jsonschema:"Name to assign to the storage"`
	StorageType     string `json:"storageType" jsonschema:"Type of storage (sqlite or proxy)"`
	WorkDir         string `json:"workDir,omitempty" jsonschema:"Work directory for SQLite"`
}

// RegisterStorage handles the register_storage tool.
func (s *Server) RegisterStorage(ctx context.Context, req *mcp.CallToolRequest, in RegisterStorageInput) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.storages[in.Name]; ok {
		return nil, nil, fmt.Errorf("storage %q already exists", in.Name)
	}

	config := types.StorageConfig{
		StorageType:     in.StorageType,
		StorageDir:      in.WorkDir,
	}

	s.storages[in.Name] = &storageEntry{cfg: config}
	type Payload struct {
		Message string `json:"message"`
	}
	return nil, Payload{Message: fmt.Sprintf("Storage %q registered successfully", in.Name)}, nil
}
