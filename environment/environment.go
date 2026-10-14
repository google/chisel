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

// Package environment extracts and infers information related to the git
// environment.
package environment

import (
	"os"
	"path"
	"path/filepath"

	"chisel/constants"
	"chisel/exec"
	"chisel/git"
	"chisel/types"
)

// New returns an initialized version of an Environment based on the
// current working directory.
func New(workDir string, projects []types.NormalizedProjectConfig) (*types.Environment, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	gitClient := &git.Client{
		Path:     cwd,
		Executor: &exec.Client{},
	}

	gitRoot, err := gitClient.Root()
	if err != nil {
		return nil, err
	}
	gitClient.Path = gitRoot

	head, err := gitClient.Head()
	if err != nil {
		return nil, err
	}
	relPath, err := filepath.Rel(path.Join(workDir, constants.ReposDir), gitRoot)
	if err != nil {
		return nil, err
	}
	var projName string
	for _, project := range projects {
		if project.Path() == relPath {
			projName = relPath
			break
		}
	}

	return &types.Environment{
		CWD:     cwd,
		GitRoot: gitRoot,
		HEAD:    head,
		Project: projName,
	}, nil
}
