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
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"flag"
	"log"
	"github.com/google/subcommands"
	"chisel/cache"
	"chisel/constants"
	"chisel/contents"
	"chisel/handlers"
	"chisel/types"
)

const (
	contentsFileHelp = `File to analyze for content diffs.`

	contentsFilelistHelp = `List of files to use for content diffs. "-" can be
	specified to read from stdin.`
)

// ContentsCmd implements the subcommands interface for the "contents"
// subcommand.
type ContentsCmd struct {
	file     string
	filelist string
}

// Name returns the subcommand name.
func (c *ContentsCmd) Name() string { return "contents" }

// Synopsis describes the subcommand behavior.
func (c *ContentsCmd) Synopsis() string {
	return "Collect content diffs on an already initialized 'repo' or 'git' workarea."
}

// Usage describes the subcommand usage.
func (c *ContentsCmd) Usage() string {
	return "contents <flags>\n"
}

// SetFlags describes the subcommand flags.
func (c *ContentsCmd) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.file, "file", "", contentsFileHelp)
	f.StringVar(&c.filelist, "filelist", "", contentsFilelistHelp)
}

func filesFromReader(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)

	var files []string
	for scanner.Scan() {
		files = append(files, scanner.Text())
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("Error: unable to scan input : %v", scanner.Err())
	}

	return files, nil
}

func createFileList(filelist string) ([]string, error) {
	if filelist == "" {
		return nil, nil
	}

	// From stdin.
	if filelist == "-" {
		return filesFromReader(os.Stdin)
	}

	// From a filelist.
	file, err := os.Open(filelist)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return filesFromReader(file)
}

// Execute forwards the flags and options to the appropriate handler.
func (c *ContentsCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
	if len(opts) == 0 {
		log.Printf("Missing Opts argument.")
		return subcommands.ExitUsageError
	}

	o := opts[0].(*Opts)
	config, err := loadConfigWithOverrides(o)
	if err != nil {
		log.Printf("loadConfigWithOverrides failed: %v", err)
		return subcommands.ExitFailure
	}

	storage, err := storageFactory(ctx, types.StorageConfig{
		StorageType:     config.StorageType,
		StorageDir:      config.WorkDir,
	})
	if err != nil {
		log.Printf("storageFactory failed: %v", err)
		return subcommands.ExitFailure
	}
	defer storage.Close()

	fileCache, err := cache.New(path.Join(config.WorkDir, constants.CacheDir))
	if err != nil {
		log.Fatalf("Couldn't create file cache: %v", err)
		return subcommands.ExitFailure
	}
	var writeAheadCache handlers.Cache
	contentsOptions := &handlers.ContentsOptions{
		ApplicationConfig: config,
		WriteAheadCache:   writeAheadCache,
		Cache:             fileCache,
		Storage:           storage,
		GitClientCreator: func(path string, maxWorkers int) handlers.GitClient {
			return gitClientCreator(path, 1)
		},
		ContentsDifferCreator: func(opts *contents.DifferOpts) (handlers.ContentsDiffer, error) {
			return contents.New(opts)
		},
	}

	contentsOptions.Files, err = createFileList(c.filelist)
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}
	if c.file != "" {
		contentsOptions.Files = append(contentsOptions.Files, c.file)
	}

	if err := handlers.Contents(ctx, contentsOptions); err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}
