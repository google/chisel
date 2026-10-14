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

// Package stats parses and collects statistics between two git branches.
// Methods of Differ should not be called concurrently.
package stats

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"log"
	"golang.org/x/sync/errgroup"
	"github.com/cespare/xxhash/v2"
	"chisel/commits"
	"chisel/constants"
	"chisel/types"
)

// AddStatsAction can adds uniqueness to a Project Key hash.
const AddStatsAction = "AddStats"

// Differ collects stats for a single project.
type Differ struct {
	opts *DifferOpts

	maxWorkersResolved int

	keyPrefix string

	// Map of commit to its merge base.
	mergeBases map[string]string

	uniqueFirstParentCommits        []string
	uniqueFirstParentMergeCommits   []string
	uniqueFirstParentNoMergeCommits []string

	commitToOrderID map[string]int64

	excludeRefs []string

	// The map is indexed by CommitSha1 X, and Y's presence in the
	// mapped-to slice indicates that Y is uniquely reachable from X.
	uniqueReachableCommitsFromMerges map[string][]string

	commitObjects *CommitObjects
	statObjects   *StatObjects

	areCommitsPending bool
}

// New returns a new Differ.
func New(opts *DifferOpts) (*Differ, error) {
	if opts.GitClient == nil {
		return nil, errors.New("received nil GitClient")
	}

	return &Differ{
		opts:            opts,
		mergeBases:      make(map[string]string),
		commitToOrderID: make(map[string]int64),
		commitObjects: &CommitObjects{
			Commits:       make(map[string]*Commit),
			BranchCommits: make(map[string]*BranchCommit),
		},
	}, nil
}

func (d *Differ) checkConfig() error {
	if d.opts == nil {
		return errors.New("opts is nil")
	}

	if d.opts.ProjectConfig == nil {
		return errors.New("ProjectConfig is nil")
	}

	if !d.opts.ProjectConfig.ValuesSet() {
		return errors.New("ProjectConfig.ValuesSet() is false")
	}

	return nil
}

func (d *Differ) createKeyPrefix() error {
	var buffer bytes.Buffer
	enc := gob.NewEncoder(&buffer)

	log.Printf("Calling RefToSha on ProjectConfig.DownstreamBranch for project %s...", d.projectName())
	downstreamHeadCommit, err := d.opts.GitClient.RefToSha(d.opts.ProjectConfig.DownstreamRef())
	if err != nil {
		return fmt.Errorf("RefToSha(%s) failed for project %q : %w", d.opts.ProjectConfig.DownstreamRef(), d.projectName(), err)
	}

	sinceCommit := ""
	if d.opts.ProjectConfig.Since.Commit != constants.BootstrapValue && d.opts.ProjectConfig.Since.Commit != "" {
		log.Printf("Calling RefToSha on ProjectConfig.Since.Commit for project %s...", d.projectName())
		sinceCommit, err = d.opts.GitClient.RefToSha(d.opts.ProjectConfig.Since.Commit)
		if err != nil {
			return fmt.Errorf("RefToSha(%s) failed for project %q : %w", d.opts.ProjectConfig.Since.Commit, d.projectName(), err)
		}
	}

	skipCommitsDeterministic := make([]string, len(d.opts.SkipCommits))
	{
		i := 0
		for c := range d.opts.SkipCommits {
			skipCommitsDeterministic[i] = c
			i++
		}
	}
	sort.Strings(skipCommitsDeterministic)

	enc.Encode(struct {
		ProjectConfig *types.ProjectConfig
		HeadCommit    string
		SinceCommit   string
		SkipCommits   []string
		MaxCommits    int
	}{
		ProjectConfig: d.opts.ProjectConfig,
		HeadCommit:    downstreamHeadCommit,
		SinceCommit:   sinceCommit,
		SkipCommits:   skipCommitsDeterministic,
		MaxCommits:    d.opts.MaxCommits,
	})
	d.keyPrefix = strconv.FormatUint(xxhash.Sum64(buffer.Bytes()), 10)

	return nil
}

func (d *Differ) populateMergeBases(mergeBases []string) bool {
	foundMergeBase := false
	for _, mb := range mergeBases {
		if mb != "" {
			foundMergeBase = true
			break
		}
	}

	if !foundMergeBase {
		return false
	}

	for idx, m := range mergeBases {
		c := d.uniqueFirstParentCommits[idx]
		d.mergeBases[c] = m
	}

	return true
}

func (d *Differ) projectName() string {
	return d.opts.ProjectConfig.Name
}

func (d *Differ) cachePut(key string, value any) error {
	// Not viewing a nil cache as an error.  It's just a loss
	// of performance.
	if d.opts.Cache == nil {
		return nil
	}
	return d.opts.Cache.Put(key, value)
}

func (d *Differ) cacheGet(key string, out any) error {
	// Treat a nil cache as a miss
	if d.opts.Cache == nil {
		return errors.New("nil cache")
	}
	return d.opts.Cache.Get(key, out)
}

func (d *Differ) populateUniqueFirstParentMergeCommits() error {
	var err error
	key := d.keyPrefix + "_UniqueFirstParentMergeCommits"
	if err = d.cacheGet(key, &d.uniqueFirstParentMergeCommits); err == nil { // if NO error
		return nil
	}

	descendant := d.uniqueFirstParentCommits[len(d.uniqueFirstParentCommits)-1]
	if d.uniqueFirstParentMergeCommits, err = d.opts.GitClient.UniqueFirstParentMergeCommits(descendant, d.excludeRefs); err != nil {
		return fmt.Errorf("UniqueFirstParentMergeCommits for project %q : %w", d.projectName(), err)
	}

	if err = d.cachePut(key, &d.uniqueFirstParentMergeCommits); err != nil {
		return fmt.Errorf("unable to add uniqueFirstParentMergeCommits to cache for project %q : %w", d.projectName(), err)
	}

	return nil
}

func (d *Differ) populateUniqueFirstParentNoMergeCommits() error {
	var err error
	key := d.keyPrefix + "_UniqueFirstParentNoMergeCommits"
	if err = d.cacheGet(key, &d.uniqueFirstParentNoMergeCommits); err == nil { // if NO error
		return nil
	}

	descendant := d.uniqueFirstParentCommits[len(d.uniqueFirstParentCommits)-1]
	if d.uniqueFirstParentNoMergeCommits, err = d.opts.GitClient.UniqueFirstParentNoMergeCommits(descendant, d.excludeRefs); err != nil {
		return fmt.Errorf("UniqueFirstParentNoMergeCommits for project %q : %w", d.projectName(), err)
	}

	if err = d.cachePut(key, &d.uniqueFirstParentNoMergeCommits); err != nil {
		return fmt.Errorf("unable to add uniqueFirstParentNoMergeCommits to cache for project %s : %w", d.projectName(), err)
	}

	return nil
}

func (d *Differ) populateUniqueReachableCommitsFromMerges(ctx context.Context) error {
	var err error
	key := d.keyPrefix + "_UniqueReachableCommitsFromMergesOptimized"
	if err = d.cacheGet(key, &d.uniqueReachableCommitsFromMerges); err == nil { // if NO error
		return nil
	}

	if d.uniqueReachableCommitsFromMerges, err = d.opts.GitClient.UniqueReachableCommitsFromMergesOptimized(ctx, d.uniqueFirstParentMergeCommits, d.excludeRefs); err != nil {
		return fmt.Errorf("UniqueReachableCommitsFromMerges for project %s : %w", d.projectName(), err)
	}

	if err = d.cachePut(key, &d.uniqueReachableCommitsFromMerges); err != nil {
		return fmt.Errorf("unable to add uniqueReachableCommitsFromMerges to cache for project %s : %w", d.projectName(), err)
	}

	return nil
}

func (d *Differ) maybeRemapEmail(email string) (string, error) {
	if d.opts.EmailMapper == nil {
		return email, nil
	}

	return d.opts.EmailMapper(email)
}

func (d *Differ) createCommitObject(commit string, commitType constants.CommitType, commitInfo *types.CommitInfo) (*Commit, error) {
	email, err := d.maybeRemapEmail(commitInfo.Email)
	// It's not an application error for an email to not exist.
	if err != nil {
		log.Printf("Project: %s, Commit: %s : %v", d.projectName(), commit, err)
	}

	c := &Commit{}
	c.CommitSha1 = commit
	c.CommitType = commitType
	c.Email = email
	c.CommitTime = commitInfo.CommitTime
	c.FirstParent = commitInfo.FirstParent
	c.SecondParent = commitInfo.SecondParent
	c.TreeHash = commitInfo.TreeHash
	c.Subject = commitInfo.Subject
	c.Body = commitInfo.Body
	c.ChangeID = commitInfo.ChangeID
	c.Bugs = commitInfo.Bugs
	c.CRsFixed = commitInfo.CRsFixed

	return c, nil
}

func (d *Differ) maybeUpdateCommitsObjectWithInfo(mu *sync.Mutex, commit string, commitInfo *types.CommitInfo, isMergeCommit bool) error {
	if _, exists := d.opts.SkipCommits[commit]; exists {
		return nil
	}

	commitType := constants.CommitTypeUnset
	if isMergeCommit {
		commitType = constants.CommitTypeMerge
	} else {
		isRevertCommit, err := d.opts.GitClient.IsRevertCommit(commit)
		if err != nil {
			return err
		}

		if isRevertCommit {
			commitType = constants.CommitTypeRevert
		} else {
			commitType = constants.CommitTypeStandard
		}
	}

	c, err := d.createCommitObject(commit, commitType, commitInfo)
	if err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()
	d.commitObjects.Commits[commit] = c

	return nil
}

func (d *Differ) populateFirstParentCommitObjectsSansFileStats(ctx context.Context, doMergeCommits bool) error {
	var firstParentCommits []string
	if doMergeCommits {
		firstParentCommits = d.uniqueFirstParentMergeCommits
	} else {
		firstParentCommits = d.uniqueFirstParentNoMergeCommits
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(min(d.maxWorkersResolved, len(firstParentCommits)))

	var commitsMu sync.Mutex
	var branchCommitsMu sync.Mutex
	for _, commit := range firstParentCommits {

		eg.Go(func() error {
			if err := egCtx.Err(); err != nil {
				return err
			}

			commitInfo, err := d.opts.GitClient.CommitInfo(commit)
			if err != nil {
				return err
			}

			if err := d.maybeUpdateCommitsObjectWithInfo(&commitsMu, commit, commitInfo, doMergeCommits); err != nil {
				return err
			}

			mergeBase, exists := d.mergeBases[commit]
			if !exists {
				return fmt.Errorf("unable to lookup merge base for commit: %q", commit)
			}

			bc := &BranchCommit{
				CommitSha1:    commit,
				IsFirstParent: true,
				MergeBase:     mergeBase,
				ResidentTime:  commitInfo.CommitTime,
				OrderID:       d.commitToOrderID[commit],
			}

			branchCommitsMu.Lock()
			d.commitObjects.BranchCommits[commit] = bc
			branchCommitsMu.Unlock()

			return nil
		})
		if egCtx.Err() != nil {
			break
		}
	}

	return eg.Wait()
}

func (d *Differ) maybeUpdateCommitsObject(mu *sync.Mutex, commit string) error {
	if _, exists := d.opts.SkipCommits[commit]; exists {
		return nil
	}

	commitInfo, err := d.opts.GitClient.CommitInfo(commit)
	if err != nil {
		return err
	}

	commitType, err := d.opts.GitClient.CommitType(commit)
	if err != nil {
		return err
	}
	c, err := d.createCommitObject(commit, commitType, commitInfo)
	if err != nil {
		return err
	}
	mu.Lock()
	d.commitObjects.Commits[commit] = c
	mu.Unlock()

	return nil
}

func (d *Differ) populateNonFirstParentCommitObjectsSansFileStats(ctx context.Context) error {
	eg, egCtx := errgroup.WithContext(ctx)

	type WorkUnit struct {
		descendant string
		ancestors  []string
	}

	work := make(chan *WorkUnit)

	go func() {
		for descendant, ancestors := range d.uniqueReachableCommitsFromMerges {
			work <- &WorkUnit{descendant, ancestors}
		}
		close(work)
	}()

	numWorkers := min(d.maxWorkersResolved, len(d.uniqueReachableCommitsFromMerges))
	var commitsMu sync.Mutex
	var branchCommitsMu sync.Mutex
	for i := 0; i < numWorkers; i++ {
		eg.Go(func() error {
			for {
				select {
				case workUnit, ok := <-work:
					if !ok {
						return nil
					}

					for _, ancestor := range workUnit.ancestors {
						if err := d.maybeUpdateCommitsObject(&commitsMu, ancestor); err != nil {
							return err
						}

						bc := &BranchCommit{
							CommitSha1:       ancestor,
							IsFirstParent:    false,
							OldestDescendant: workUnit.descendant,
						}

						branchCommitsMu.Lock()
						bc.ResidentTime = d.commitObjects.BranchCommits[workUnit.descendant].ResidentTime
						d.commitObjects.BranchCommits[ancestor] = bc
						branchCommitsMu.Unlock()
					}
				case <-egCtx.Done():
					return egCtx.Err()
				}
			}
		})
	}

	return eg.Wait()
}

func (d *Differ) populateCommitObjectsSansFileStats(ctx context.Context) error {
	var err error
	key := d.keyPrefix + "_populateCommitObjectsSansFileStats"
	if err = d.cacheGet(key, &d.commitObjects); err == nil { // if NO error
		return nil
	}

	if err = d.populateFirstParentCommitObjectsSansFileStats(ctx, true); err != nil {
		return fmt.Errorf("populateFirstParentCommitObjectsSansFileStats for merges for project %s : %w", d.projectName(), err)
	}

	if err = d.populateFirstParentCommitObjectsSansFileStats(ctx, false); err != nil {
		return fmt.Errorf("populateFirstParentCommitObjectsSansFileStats for non-merges for project %s : %w", d.projectName(), err)
	}

	if err = d.populateNonFirstParentCommitObjectsSansFileStats(ctx); err != nil {
		return fmt.Errorf("populateNonFirstParentCommitObjectsSansFileStats for project %s : %w", d.projectName(), err)
	}

	if err = d.cachePut(key, &d.commitObjects); err != nil {
		return fmt.Errorf("unable to add commitObjects to cache for project %s : %w", d.projectName(), err)
	}

	return nil
}

func (d *Differ) commitStatsForCommitType(commit string, commitType constants.CommitType) (*types.CommitStats, error) {
	if commitType == constants.CommitTypeMerge {
		return d.opts.GitClient.CommitStatsFromCombinedDiff(commit)
	}

	return d.opts.GitClient.CommitStats(commit)
}

// finishCommitsAndFiles populates file stats in Commits and File objects.
// These branch agnostic file stats can be populated concurrently.
func (d *Differ) finishCommitsAndFiles(ctx context.Context) error {
	eg, egCtx := errgroup.WithContext(ctx)
	work := make(chan *Commit)

	go func() {
		for _, commit := range d.commitObjects.Commits {
			work <- commit
		}
		close(work)
	}()

	numWorkers := min(d.maxWorkersResolved, len(d.commitObjects.Commits))
	var mu sync.Mutex
	for i := 0; i < numWorkers; i++ {
		eg.Go(func() error {
			for {
				select {
				case commit, ok := <-work:
					if !ok {
						return nil
					}

					commitStats, err := d.commitStatsForCommitType(commit.CommitSha1, commit.CommitType)
					if err != nil {
						return err
					}
					commit.FilesChanged = commitStats.ShortStat.FilesChanged
					commit.Insertions = commitStats.ShortStat.Insertions
					commit.Deletions = commitStats.ShortStat.Deletions

					for _, numStatLine := range commitStats.NumStats {
						f := &File{
							Path:       numStatLine.File,
							CommitSha1: commit.CommitSha1,
							Insertions: numStatLine.Insertions,
							Deletions:  numStatLine.Deletions,
						}
						mu.Lock()
						slicePtr, exists := d.statObjects.FileObjects.Files[commit.CommitSha1]
						if !exists {
							slicePtr = &[]*File{}
							d.statObjects.FileObjects.Files[commit.CommitSha1] = slicePtr
						}
						*slicePtr = append(*slicePtr, f)
						mu.Unlock()
					}
				case <-egCtx.Done():
					return egCtx.Err()
				}
			}
		})
	}

	return eg.Wait()
}

func (d *Differ) insertBranchFile(file, commit string, branchFile *BranchFile) {
	d.opts.MostRecentBranchFiles[file] = branchFile
	slicePtr, exists := d.statObjects.FileObjects.BranchFiles[commit]
	if !exists {
		slicePtr = &[]*BranchFile{}
		d.statObjects.FileObjects.BranchFiles[commit] = slicePtr
	}
	*slicePtr = append(*slicePtr, branchFile)
}

// finishBranchCommitsAndBranchFiles populates file stats in BranchCommit and
// BranchFile objects. For BranchFileObjects, FirstParent Commits must be
// walked serially in chronological order so that MostRecentBranchFiles is
// populated correctly.
func (d *Differ) finishBranchCommitsAndBranchFiles(ctx context.Context) error {
	eg, egCtx := errgroup.WithContext(ctx)
	work := make(chan *BranchCommit)

	go func() {
		for _, commit := range d.uniqueFirstParentCommits {
			// A uniqueFirstParentCommit may have an empty merge base and should
			// be skipped. This can be due to an initial commit
			// that doesn't have a merge base with the upstream.
			if branchCommit := d.commitObjects.BranchCommits[commit]; branchCommit.MergeBase != "" {
				work <- branchCommit
			} else {
				log.Printf("Project: %s, Commit: %s has an empty merge base", d.projectName(), commit)
			}
		}
		close(work)
	}()

	eg.Go(func() error {
		// Keep track of most recent delta of a file when walking commits in order
		// and only store a change if there's a different delta to the file.
		if d.opts.MostRecentBranchFiles == nil {
			d.opts.MostRecentBranchFiles = make(map[string]*BranchFile)
		}
		for {
			select {
			case branchCommit, ok := <-work:
				if !ok {
					return nil
				}

				// Need to detect files that no longer show up in --numstat and have now converged.
				notSeenThisCommit := make(map[string]bool)
				for file, branchFile := range d.opts.MostRecentBranchFiles {
					if branchFile.NetInsertions != 0 || branchFile.NetDeletions != 0 {
						notSeenThisCommit[file] = true
					}
				}

				diffStats, err := d.opts.GitClient.CommitDiffStats(branchCommit.MergeBase, branchCommit.CommitSha1)
				if err != nil {
					return err
				}
				branchCommit.NetFilesChanged = diffStats.ShortStat.FilesChanged
				branchCommit.NetInsertions = diffStats.ShortStat.Insertions
				branchCommit.NetDeletions = diffStats.ShortStat.Deletions

				for _, numStatLine := range diffStats.NumStats {
					branchFile, existsMostRecent := d.opts.MostRecentBranchFiles[numStatLine.File]
					if _, exists := notSeenThisCommit[numStatLine.File]; exists {
						delete(notSeenThisCommit, numStatLine.File)
					}

					hasDelta := false
					if existsMostRecent {
						if branchFile.NetInsertions != numStatLine.Insertions || branchFile.NetDeletions != numStatLine.Deletions {
							hasDelta = true
						}
					}

					// Only create new entries if one currently doesn't exist, or if there's a delta
					// with the most recent diff.
					if !existsMostRecent || hasDelta {
						// Intentional error ignore:
						// A deleted file will fail on this call,
						// but the return value of '0' is the
						// correct value to use.
						numLinesInFile, _ := d.opts.GitClient.LinesInFileAtCommit(numStatLine.File, branchCommit.MergeBase)
						branchFile = &BranchFile{
							Path:           numStatLine.File,
							CommitSha1:     branchCommit.CommitSha1,
							NetInsertions:  numStatLine.Insertions,
							NetDeletions:   numStatLine.Deletions,
							NumLinesInFile: numLinesInFile,
						}

						d.insertBranchFile(numStatLine.File, branchCommit.CommitSha1, branchFile)
					}
				}

				// Take care of commits that have converged.
				for file := range notSeenThisCommit {
					// Intentional error ignore:
					// A deleted file will fail on this call,
					// but the return value of '0' is the
					// correct value to use.
					numLinesInFile, _ := d.opts.GitClient.LinesInFileAtCommit(file, branchCommit.MergeBase)
					branchFile := &BranchFile{
						Path:           file,
						CommitSha1:     branchCommit.CommitSha1,
						NetInsertions:  0,
						NetDeletions:   0,
						NumLinesInFile: numLinesInFile,
					}
					d.insertBranchFile(file, branchCommit.CommitSha1, branchFile)
				}
			case <-egCtx.Done():
				return egCtx.Err()
			}
		}
	})

	return eg.Wait()
}

func (d *Differ) finishCommitAndFileObjectsWork(ctx context.Context) error {
	d.statObjects = &StatObjects{
		CommitObjects: d.commitObjects,
		FileObjects: &FileObjects{
			Files:       make(map[string]*[]*File),
			BranchFiles: make(map[string]*[]*BranchFile),
		},
	}

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return d.finishCommitsAndFiles(egCtx)
	})

	eg.Go(func() error {
		return d.finishBranchCommitsAndBranchFiles(egCtx)
	})

	return eg.Wait()
}

func (d *Differ) finishCommitAndFileObjects(ctx context.Context) error {
	key := d.keyPrefix + "_finishCommitAndFileObjects"
	if err := d.cacheGet(key, &d.statObjects); err == nil { // if NO error
		return nil
	}

	if err := d.finishCommitAndFileObjectsWork(ctx); err != nil {
		return fmt.Errorf("finishCommitAndFileObjects for project %s : %w", d.projectName(), err)
	}

	if err := d.cachePut(key, &d.statObjects); err != nil {
		return fmt.Errorf("unable to add statObjects to cache for project %s : %w", d.projectName(), err)
	}

	return nil
}

func (d *Differ) populateOrderIds(start int64) {
	for idx, commit := range d.uniqueFirstParentCommits {
		d.commitToOrderID[commit] = start + int64(idx)
	}
}

func (d *Differ) analyzeRangeInfo() string {
	var sb strings.Builder
	length := len(d.uniqueFirstParentCommits)

	fmt.Fprintf(&sb, "project: %s", d.projectName())
	fmt.Fprintf(&sb, " startCommit: %s", d.uniqueFirstParentCommits[0])
	fmt.Fprintf(&sb, " startCommitOrderId: %d", d.opts.ProjectConfig.Since.NextStartID())
	fmt.Fprintf(&sb, " endCommit: %s", d.uniqueFirstParentCommits[length-1])
	fmt.Fprintf(&sb, " endCommitOrderId: %d", d.opts.ProjectConfig.Since.NextStartID()+int64(length-1))

	return sb.String()
}

// Analyze analyzes a repo and returns the StatObjects.
// Shallow git repos will be skipped.
func (d *Differ) Analyze(ctx context.Context) (*StatObjects, error) {
	var err error

	if err = d.checkConfig(); err != nil {
		return nil, err
	}

	if d.opts.GitClient.IsShallow() {
		log.Printf("Skipping shallow project %v", d.opts.ProjectConfig)
		return nil, nil
	}

	d.maxWorkersResolved = max(1, d.opts.MaxWorkers)

	if err := d.createKeyPrefix(); err != nil {
		return nil, err
	}

	d.excludeRefs = append(d.excludeRefs, d.opts.ProjectConfig.UpstreamRef())
	if d.opts.ProjectConfig.Since.Commit != constants.BootstrapValue && d.opts.ProjectConfig.Since.Commit != "" {
		d.excludeRefs = append(d.excludeRefs, d.opts.ProjectConfig.Since.Commit)
	}

	log.Printf("Calling UniqueFirstParentCommits for project %s...", d.projectName())
	uOpts := &commits.UniqueFirstParentCommitsOptions{
		Ref:         d.opts.ProjectConfig.DownstreamRef(),
		ExcludeRefs: d.excludeRefs,
		Cache:       d.opts.Cache,
		GitClient:   d.opts.GitClient,
	}
	if d.uniqueFirstParentCommits, err = commits.UniqueFirstParentCommits(uOpts); err != nil {
		return nil, fmt.Errorf("UniqueFirstParentCommits for project %s : %w", d.projectName(), err)
	}
	if d.opts.MaxCommits > 0 {
		end := min(len(d.uniqueFirstParentCommits), d.opts.MaxCommits)
		d.areCommitsPending = end < len(d.uniqueFirstParentCommits)
		d.uniqueFirstParentCommits = d.uniqueFirstParentCommits[:end]
	}

	if len(d.uniqueFirstParentCommits) == 0 {
		log.Printf("There aren't any new commits for project %s.", d.projectName())
		return nil, nil
	}

	log.Printf("Calling MergeBases for project %s...", d.projectName())
	mOpts := &commits.MergeBasesOptions{
		UniqueFirstParentCommits: d.uniqueFirstParentCommits,
		UpstreamRef:              d.opts.ProjectConfig.UpstreamRef(),
		MaxWorkers:               d.maxWorkersResolved,
		Cache:                    d.opts.Cache,
		GitClient:                d.opts.GitClient,
	}
	mergeBases, err := commits.MergeBases(ctx, mOpts)
	if err != nil {
		return nil, fmt.Errorf("MergeBases for project %s : %w", d.projectName(), err)
	}

	if d.populateMergeBases(mergeBases) == false {
		log.Printf("There aren't any merge bases for project %s.", d.projectName())
		return nil, nil
	}

	d.populateOrderIds(d.opts.ProjectConfig.Since.NextStartID())

	log.Printf("Calling populateUniqueFirstParentMergeCommits for project %s...", d.projectName())
	if err := d.populateUniqueFirstParentMergeCommits(); err != nil {
		return nil, err
	}

	log.Printf("Calling populateUniqueFirstParentNoMergeCommits for project %s...", d.projectName())
	if err := d.populateUniqueFirstParentNoMergeCommits(); err != nil {
		return nil, err
	}

	log.Printf("Calling populateUniqueReachableCommitsFromMerges for project %s...", d.projectName())
	if err := d.populateUniqueReachableCommitsFromMerges(ctx); err != nil {
		return nil, err
	}

	rangeInfo := d.analyzeRangeInfo()
	log.Printf("Calling populateCommitObjectsSansFileStats for %s", rangeInfo)
	if err := d.populateCommitObjectsSansFileStats(ctx); err != nil {
		return nil, err
	}

	log.Printf("Calling finishCommitAndFileObjects for project %s", rangeInfo)
	if err := d.finishCommitAndFileObjects(ctx); err != nil {
		return nil, err
	}

	d.statObjects.AllCommitsConsumed = (d.opts.MaxCommits <= 0) || !d.areCommitsPending
	return d.statObjects, nil
}

// KeyForProjectStats provides a key for
// (DownstreamHost, DownstreamBranch, UpstreamHost, UpstreamBranch, Project)
// It cannot be used to differentiate specific SHAs and should not be used for
// scenarios where that is a requirement.
func KeyForProjectStats(projectConfig *types.ProjectConfig) string {
	var buffer bytes.Buffer
	enc := gob.NewEncoder(&buffer)

	enc.Encode(struct {
		Action           string
		DownstreamHost   string
		DownstreamBranch string
		UpstreamHost     string
		UpstreamBranch   string
		Project          string
	}{
		Action:           AddStatsAction,
		DownstreamHost:   projectConfig.DownstreamHost,
		DownstreamBranch: projectConfig.DownstreamBranch,
		UpstreamHost:     projectConfig.UpstreamHost,
		UpstreamBranch:   projectConfig.UpstreamBranch,
		Project:          projectConfig.Name,
	})
	return strconv.FormatUint(xxhash.Sum64(buffer.Bytes()), 10)
}

// Checksum returns the checksum of stat objects.
// Would have preferred to use encoding/gob, but it isn't deterministic when
// maps are involved.
func Checksum(statObjects *StatObjects) (uint64, error) {
	b, err := json.Marshal(statObjects)
	if err != nil {
		return 0, err
	}

	return xxhash.Sum64(b), nil
}
