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
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"flag"
	"log"
	"github.com/google/slothfs/cookie"
	"github.com/google/subcommands"
	"github.com/go-yaml/yaml"
	"chisel/flags"
	"chisel/gerrit"
	"chisel/types"
)

// ManifestCmd implements the subcommands interface for the "manifest"
// subcommand.
type ManifestCmd struct {
	manifest      string
	includeGroups flags.MultiString
	excludeFile   string

	host       string
	project    string
	ref        string
	file       string
	cookieFile string
}

// Name returns the subcommand name.
func (m *ManifestCmd) Name() string { return "manifest" }

// Synopsis describes the subcommand behavior.
func (m *ManifestCmd) Synopsis() string {
	return "Generate a new config file with projects from a local or remote manifest file and a base config file"
}

// Usage describes the subcommand usage.
func (m *ManifestCmd) Usage() string {
	return "manifest <flags>"
}

// SetFlags describes the subcommand flags.
func (m *ManifestCmd) SetFlags(f *flag.FlagSet) {
	f.Var(&m.includeGroups, "g", "Only include projects present in the provided groups")
	f.StringVar(&m.excludeFile, "e", "", "Path to exclude file with list of project names separated by newline")

	// Provided manifest file.
	f.StringVar(&m.manifest, "m", "", "Path to a local manifest file")

	// Acquire the manifest file.
	f.StringVar(&m.host, "h", "", "Gerrit host where the remote manifest file exists")
	f.StringVar(&m.project, "p", "", "Project where the remote manifest file exists")
	f.StringVar(&m.ref, "r", "", "Git reference associated with the remote manifest")
	f.StringVar(&m.file, "f", "", "Relative manifest file path from the provided project root")
	f.StringVar(&m.cookieFile, "c", "", "Path to the cookie file specified in Mozilla/Netscape format")
}

// Host can be specified with or without a leading http[s]://
func parseCookies(file, host string) ([]*http.Cookie, error) {
	jar, err := cookie.NewJar(file)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if u.Host == "" {
		u.Host = u.Path
		u.Path = ""
	}
	return jar.Cookies(u), nil
}

// Execute forwards the flags and options to the appropriate handler.
func (m *ManifestCmd) Execute(ctx context.Context, f *flag.FlagSet, opts ...any) subcommands.ExitStatus {
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

	manifest, err := m.parseManifest()
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	filtered, err := filterManifest(manifest, m.includeGroups, m.excludeFile)
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}

	projects, err := manifestToProjects(filtered)
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}
	config.Projects = projects

	b, err := yaml.Marshal(config)
	if err != nil {
		log.Print(err)
		return subcommands.ExitFailure
	}
	fmt.Println(string(b))

	return subcommands.ExitSuccess
}

func (m *ManifestCmd) parseManifest() (*Manifest, error) {
	var (
		manifest *Manifest
		err      error
	)
	if m.manifest != "" {
		manifest, err = parseManifestFile(m.manifest)
		if err != nil {
			return nil, err
		}
	} else {
		var cookies []*http.Cookie
		if m.cookieFile != "" {
			c, err := parseCookies(m.cookieFile, m.host)
			if err != nil {
				return nil, err
			}
			cookies = c
		}

		opts := gerrit.WriteFileOpts{
			Host:       m.host,
			Project:    m.project,
			Ref:        m.ref,
			File:       m.file,
			Cookies:    cookies,
			NumRetries: 1,
		}

		manifest, err = parseManifestFromGerrit(opts)
		if err != nil {
			return nil, err
		}
	}

	return manifest, nil
}

func manifestToProjects(manifest *Manifest) ([]types.NormalizedProjectConfig, error) {
	var projects []types.NormalizedProjectConfig
	for _, project := range manifest.Project {
		projects = append(projects, types.NormalizedProjectConfig{
			Name:   project.Name,
			PathIn: project.Path,
		})
	}

	return projects, nil
}

func parseManifestBytes(content []byte) (*Manifest, error) {
	var manifest *Manifest
	if err := xml.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	for _, project := range manifest.Project {
		splits := strings.SplitSeq(project.GroupsString, ",")
		for group := range splits {
			if group == "" {
				continue
			}
			if project.Groups == nil {
				project.Groups = map[string]bool{}
			}
			project.Groups[strings.TrimSpace(group)] = true
		}
	}

	return manifest, nil
}

func parseManifestFromGerrit(opts gerrit.WriteFileOpts) (*Manifest, error) {
	buffer := new(bytes.Buffer)
	if err := gerrit.WriteFile(opts, buffer); err != nil {
		return nil, err
	}

	return parseManifestBytes(buffer.Bytes())
}

func parseManifestFile(manFile string) (*Manifest, error) {
	content, err := os.ReadFile(manFile)
	if err != nil {
		return nil, err
	}

	return parseManifestBytes(content)
}

func excludesFromFile(excludeFile string) (map[string]bool, error) {
	if excludeFile == "" {
		return nil, nil
	}
	b, err := os.ReadFile(excludeFile)
	if err != nil {
		return nil, err
	}

	excludes := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewBuffer(b))
	for scanner.Scan() {
		excludes[scanner.Text()] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return excludes, nil
}

func filterManifest(manifest *Manifest, includeGroups []string, excludeFile string) (*Manifest, error) {
	excludes, err := excludesFromFile(excludeFile)
	if err != nil {
		return nil, err
	}

	if len(includeGroups) == 0 && len(excludes) == 0 {
		return manifest, nil
	}

	wantGroups := make(map[string]bool)
	for _, group := range includeGroups {
		wantGroups[group] = true
	}

	filtered := &Manifest{}
	for _, project := range manifest.Project {
		if excludes[project.Name] {
			continue
		}
		if len(wantGroups) == 0 {
			filtered.Project = append(filtered.Project, project)
		} else {
			for group := range project.Groups {
				if wantGroups[group] {
					filtered.Project = append(filtered.Project, project)
					break
				}
			}
		}
	}

	return filtered, nil
}

// Project represents a single <project> in a manifest file.
type Project struct {
	Path         string          `xml:"path,attr,omitempty"`
	Name         string          `xml:"name,attr"`
	GroupsString string          `xml:"groups,attr,omitempty"`
	Groups       map[string]bool `xml:"-"`
}

// Manifest holds the list of projects present in a manifest file.
// Note that it does not traverse 'included' manifests.
type Manifest struct {
	Project []*Project `xml:"project"`
}
