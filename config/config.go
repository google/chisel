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

// Package config reads the chisel configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/go-yaml/yaml"
	"chisel/constants"
	"chisel/types"
)

// search looks for a chisel.yaml file up the directory tree.
func search(from string) (string, error) {
	curr, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}
	for {
		_, err := os.Stat(path.Join(curr, constants.DefaultConfigFile))
		if err == nil {
			return path.Clean(path.Join(curr, constants.DefaultConfigFile)), nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			if curr == "/" || curr == "" {
				return "", fs.ErrNotExist
			}
			curr = path.Dir(curr)
		} else {
			return "", err
		}
	}
}

// parse reads a config file into Config struct.
func parse(configFile string) (*types.ApplicationConfig, error) {
	config := defaultConfig()
	b, err := os.ReadFile(configFile)
	if err != nil {
		return config, err
	}
	err = yaml.Unmarshal(b, &config)

	return config, err
}

func defaultConfig() *types.ApplicationConfig {
	return &types.ApplicationConfig{
		StorageType:          "sqlite",
		MaxProjectWorkers:    8,
		MaxWorkersPerProject: 8,
	}
}

// Load populates a Config struct from the input configFile, or searches
// up the directory tree to find a configFile.
func Load(configFile string) (*types.ApplicationConfig, error) {
	if configFile == "" {
		wd, err := os.Getwd()
		if err != nil {
			return &types.ApplicationConfig{}, err
		}
		configFile, err = search(wd)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return defaultConfig(), nil
			} else {
				return &types.ApplicationConfig{}, err
			}
		}
	}

	return parse(configFile)
}

func overrideConfig(orig *types.ApplicationConfig, overrides *types.ApplicationConfig) *types.ApplicationConfig {
	final := *orig

	setIfInPresent := func(tgt *string, in string) {
		if in == "" {
			return
		}

		*tgt = in
	}

	setIfInPresent(&final.WorkDir, overrides.WorkDir)
	setIfInPresent(&final.StorageType, overrides.StorageType)
	setIfInPresent(&final.DownstreamHost, overrides.DownstreamHost)
	setIfInPresent(&final.DownstreamName, overrides.DownstreamName)
	setIfInPresent(&final.DownstreamRemote, overrides.DownstreamRemote)
	setIfInPresent(&final.DownstreamBranch, overrides.DownstreamBranch)
	setIfInPresent(&final.UpstreamHost, overrides.UpstreamHost)
	setIfInPresent(&final.UpstreamName, overrides.UpstreamName)
	setIfInPresent(&final.UpstreamRemote, overrides.UpstreamRemote)
	setIfInPresent(&final.UpstreamBranch, overrides.UpstreamBranch)

	if len(overrides.Projects) > 0 {
		final.Projects = overrides.Projects
	}

	return &final
}

// LoadWithOverrides loads the configFile applies the necessary overrides.
func LoadWithOverrides(configFile string, overrides *types.ApplicationConfig) (*types.ApplicationConfig, error) {
	initialConfig, err := Load(configFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read or find a config file: %v", err)
	}

	config := overrideConfig(initialConfig, overrides)

	return config, nil
}

// WriteConfig serializes the config to the writer.
func WriteConfig(config *types.ApplicationConfig, w io.Writer) error {
	b, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	buffer := bytes.NewBuffer(b)

	_, err = io.Copy(w, buffer)

	return err
}
