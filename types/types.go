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

// Package types provides common types used by Chisel.
package types

import (
	"fmt"
	"runtime"
	"strings"

	"chisel/constants"
)

// NumCPU is the number of logical CPUs usable by process.
var NumCPU = runtime.NumCPU()

// ProjectCommit pairs a project with a commit.
type ProjectCommit struct {
	Project string
	Commit  string
}

// SinceCommit describes a Commit that was previously analyzed.
type SinceCommit struct {
	OrderID int64
	CommitBasePair
}

// NextStartID returns the OrderID that the next Analyze run should use as
// start offset.
func (s *SinceCommit) NextStartID() int64 {
	if s.Commit == "" || s.Commit == constants.BootstrapValue {
		return 0
	}

	return s.OrderID + 1
}

// CommitBasePair pairs a commit with a merge-base.
type CommitBasePair struct {
	Commit    string
	MergeBase string
}

// Environment defines contextual information about the application environment.
type Environment struct {
	CWD     string
	GitRoot string
	HEAD    string
	Project string
}

// NormalizedProjectConfig defines project level data without
// duplication of host information.
type NormalizedProjectConfig struct {
	Name   string `yaml:"name"`
	PathIn string `yaml:"path"`
}

// Path returns name as a path if one was not provided.
func (n NormalizedProjectConfig) Path() string {
	if n.PathIn == "" {
		return n.Name
	}

	return n.PathIn
}

// BranchRef combines the remote and branch name, if applicable.
func BranchRef(remote, branch string) string {
	if remote != "" {
		return remote + "/" + branch
	}

	return branch
}

// ApplicationConfig defines the application config.
type ApplicationConfig struct {
	// Human friendly name
	// This will be used in database tables rather than DownstreamHost
	// to allow freedom of protocol. e.g. https://github.com vs. rpc://github.com
	DownstreamName string `yaml:"downstreamName"`

	// Git host. e.g. https://github.com
	DownstreamHost string `yaml:"downstreamHost"`

	DownstreamRemote string `yaml:"downstreamRemote"`

	// The branch name without leading "<remote>/"
	DownstreamBranch string `yaml:"downstreamBranch"`

	// Human friendly name
	// Same reasoning as DownstreamHostName applies
	UpstreamName string `yaml:"upstreamName"`

	// Git host. e.g. https://github.com
	UpstreamHost string `yaml:"upstreamHost"`

	UpstreamRemote string `yaml:"upstreamRemote"`

	// The branch name without leading "<remote>/"
	UpstreamBranch string `yaml:"upstreamBranch"`

	WorkDir              string                    `yaml:"workDir"`
	StorageType          string                    `yaml:"storageType"`
	Projects             []NormalizedProjectConfig `yaml:"projects"`
	MaxProjectWorkers    int                       `yaml:"maxProjectWorkers"`
	MaxWorkersPerProject int                       `yaml:"maxWorkersPerProject"`
	MaxCommitsPerProject int                       `yaml:"maxCommitsPerProject"`
	ProxyPort            int                       `yaml:"proxyPort"`
}

// StorageConfig defines the configuration needed to create a storage instance.
type StorageConfig struct {
	StorageType     string
	StorageDir      string
}

// QueryMode specifies the mode to use when querying commit data.
type QueryMode int

// The enumerated values for QueryMode.
const (
	QueryModeAll QueryMode = iota
	QueryModeCommit
	QueryModeRange
)

// QueryConfig defines fields refining dataset filtering.
type QueryConfig struct {
	ApplicationConfig *ApplicationConfig

	// Commit is intended for use as a precise qualifier. e.g. "exactly at
	// 'Commit'". This takes precedence over BeforeCommit.
	Commit string

	// BeforeCommit is intended for use as a range qualifier. e.g. "before (
	// and including) 'BeforeCommit'"
	BeforeCommit string

	File   string
	HunkID string // typed as string for a safe zero value.
	Line   int
}

// QueryMode returns the query mode as a function(Commit, BeforeCommit).
func (q *QueryConfig) QueryMode() QueryMode {
	if q.Commit != "" {
		return QueryModeCommit
	} else if q.BeforeCommit != "" {
		return QueryModeRange
	} else {
		return QueryModeAll
	}
}

// ProjectConfig specifies information unique to analysis of an
// individual project.
type ProjectConfig struct {
	DownstreamName   string
	DownstreamHost   string
	DownstreamRemote string

	// The branch name without leading "<remote>/"
	DownstreamBranch string

	UpstreamName   string
	UpstreamHost   string
	UpstreamRemote string

	// The branch name without leading "<remote>/"
	UpstreamBranch string

	Name string
	Path string

	// Only consider FirstParent commits newer than
	// `Since.Commit`.  All commits will be considered if
	// `Since.Commit` value is "BOOTSTRAP".
	Since SinceCommit
}

// ValuesSet returns true if all required values are non-empty strings.
func (p *ProjectConfig) ValuesSet() bool {
	if p.Name == "" {
		return false
	}

	if p.DownstreamBranch == "" {
		return false
	}

	if p.UpstreamBranch == "" {
		return false
	}

	return true
}

// DownstreamRef combines the downstream remote and branch name, if applicable.
func (p *ProjectConfig) DownstreamRef() string {
	return BranchRef(p.DownstreamRemote, p.DownstreamBranch)
}

// UpstreamRef combines the upstream remote and branch name, if applicable.
func (p *ProjectConfig) UpstreamRef() string {
	return BranchRef(p.UpstreamRemote, p.UpstreamBranch)
}

// HunkHeader describes a hunk header line from a unified diff.
type HunkHeader struct {
	FromStart int
	FromCount int
	ToStart   int
	ToCount   int
}

// String converts a HunkHeader to string.
func (h *HunkHeader) String() string {
	return fmt.Sprintf("FromStart: %d FromCount: %d ToStart: %d ToCount: %d", h.FromStart, h.FromCount, h.ToStart, h.ToCount)
}

// HunkInfo describes a hunk segment location and its type.
type HunkInfo struct {
	Start       int
	Count       int
	SegmentType constants.HunkSegmentType
}

// String converts a HunkInfo to a string.
func (h *HunkInfo) String() string {
	segmentType := constants.HunkSegmentTypeString(h.SegmentType)

	return fmt.Sprintf("Start: %d Count: %d SegmentType: %s", h.Start, h.Count, segmentType)
}

// ToUint64 converts HunkInfo to a minimal representation to use as a key in a
// map for cleaner serdes out of the box.
// HunkInfo is packed with this representation:
// r[30:0] = Start
// r[61:31] = Count
// r[63:62] = SegmentType
func (h *HunkInfo) ToUint64() uint64 {
	var r uint64
	r = uint64(h.SegmentType << 62)
	r = r | uint64(h.Count<<31)
	r = r | uint64(h.Start)
	return r
}

// FromUint64 reverses ToUint64.
func (h *HunkInfo) FromUint64(input uint64) {
	h.Start = int(input & 0x7FFF_FFFF)
	h.Count = int((input >> 31) & 0x7FFF_FFFF)
	h.SegmentType = constants.HunkSegmentType((input >> 62) & 0x3)
}

// DebugPrint prints a HunkInfo.
func (h *HunkInfo) DebugPrint() {
	fmt.Printf("%s\n", h)
}

// HunkSegment describes a To or From hunk segment from a unified diff.
type HunkSegment struct {
	HunkInfo
	Hash    int64
	Content []string
}

// String converts a HunkSegment to a string.
func (h *HunkSegment) String() string {
	return fmt.Sprintf("HunkInfo: %s Hash: %d Content:\n%s\n", h.HunkInfo.String(), h.Hash, strings.Join(h.Content, "\n"))
}

// DebugPrint prints a HunkSegment.
func (h *HunkSegment) DebugPrint() {
	fmt.Printf("%s\n", h)
}

// CommitInfo holds information about a git commit.
type CommitInfo struct {
	// Committer Email
	Email string

	// Unix Epoch Time
	CommitTime int64

	// Empty for initial commits
	FirstParent string

	// Empty for non-merges
	SecondParent string

	TreeHash string

	Subject string

	Body string

	ChangeID string

	Bugs []int

	CRsFixed []int
}

// NumStatLine represents a line from git --numstat output.
type NumStatLine struct {
	File       string
	Insertions int
	Deletions  int
}

// ShortStatLine represents a line from git --shortstat output.
type ShortStatLine struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// CommitStats represents the stats for a single commit.
type CommitStats struct {
	NumStats  []*NumStatLine
	ShortStat *ShortStatLine
}

// FileDiffHunkSegments holds information extracted from git diff of a single file.
type FileDiffHunkSegments struct {
	Name string

	FromHunkSegments   []*HunkSegment
	ToHunkSegments     []*HunkSegment
	SortedHunkSegments []*HunkSegment
}

// DebugPrint prints a FileDiffHunkSegments.
func (f *FileDiffHunkSegments) DebugPrint() {
	fmt.Printf("Name: %s\n", f.Name)

	fmt.Println("FromHunkSegments:")
	for _, segment := range f.FromHunkSegments {
		segment.DebugPrint()
	}

	fmt.Println("ToHunkSegments:")
	for _, segment := range f.ToHunkSegments {
		segment.DebugPrint()
	}

	fmt.Println("SortedHunkSegments:")
	for _, segment := range f.SortedHunkSegments {
		segment.DebugPrint()
	}
}

// ResultsDescriptor describes a generic set of result data for a project and range of commits.
type ResultsDescriptor struct {
	DownstreamName   string
	DownstreamBranch string
	UpstreamName     string
	UpstreamBranch   string
	Project          string
	BeginCommit      string
	EndCommit        string
	Checksum         uint64
}
