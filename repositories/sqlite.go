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

// Package repositories implements the chisel.Storage interface and abstracts away
// the underlying storage.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"chisel/constants"
	"chisel/contents"
	"chisel/stats"
	"chisel/storage"
	"chisel/truncate"
	"chisel/types"
)

const maxContentSize = 8 * 1024

// Sqlite implements the chisel.Storage interface using sqlite as the
// underlying storage.
type Sqlite struct {
	db *sql.DB
}

func init() {
	builder := func(data ...any) (storage.Storage, error) {
		if len(data) != 1 {
			return nil, fmt.Errorf("expecting an input argument")
		}
		dbPath := data[0].(string)
		return NewSqlite(dbPath)
	}
	storage.Register("sqlite", builder)
}

// NewSqlite creates a Sqlite instance with provided `dbPath`.
func NewSqlite(dbPath string) (*Sqlite, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	return &Sqlite{
		db: db,
	}, nil
}

// Init creates database tables.
func (s *Sqlite) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`
		CREATE TABLE IF NOT EXISTS Commits(
			DownstreamName TEXT,
			Project        TEXT,
			CommitSha1     TEXT,
			CommitType     INTEGER,
			Email          TEXT,
			CommitTime     INTEGER,
			FirstParent    TEXT,
			SecondParent   TEXT,
			TreeHash       TEXT,
			Subject        TEXT,
			Body           TEXT,
			ChangeID       TEXT,
			FilesChanged   INTEGER,
			Insertions     INTEGER,
			Deletions      INTEGER,
			PRIMARY KEY(DownstreamName, Project, CommitSha1));

		CREATE TABLE IF NOT EXISTS BranchCommits(
			DownstreamName   TEXT,
			DownstreamBranch TEXT,
			UpstreamName     TEXT,
			UpstreamBranch   TEXT,
			Project          TEXT,
			CommitSha1       TEXT,
			IsFirstParent    INTEGER,
			MergeBase        TEXT,
			OldestDescendant TEXT,
			ResidentTime     INTEGER,
			OrderId          INTEGER,
			NetFilesChanged  INTEGER,
			NetInsertions    INTEGER,
			NetDeletions     INTEGER,
			TotalDeltas      INTEGER GENERATED ALWAYS AS (NetInsertions + NetDeletions) STORED,
			PRIMARY KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1),
			FOREIGN KEY(DownstreamName, Project, CommitSha1) REFERENCES Commits(DownstreamName, Project, CommitSha1));

		CREATE INDEX IF NOT EXISTS first_parent_max_order_id ON BranchCommits (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			IsFirstParent,
			Project,
			OrderId DESC,
			CommitSha1
		);

		CREATE INDEX IF NOT EXISTS branch_commits_by_deltas ON BranchCommits (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			TotalDeltas DESC,
			CommitSha1 ASC
		);

		CREATE TABLE IF NOT EXISTS Files(
			DownstreamName TEXT,
			Project        TEXT,
			CommitSha1     TEXT,
			Path           TEXT,
			Insertions     INTEGER,
			Deletions      INTEGER,
			PRIMARY KEY(DownstreamName, Project, CommitSha1, Path),
			FOREIGN KEY(DownstreamName, Project, CommitSha1) REFERENCES Commits(DownstreamName, Project, CommitSha1));

		CREATE TABLE IF NOT EXISTS BranchFiles(
			DownstreamName   TEXT,
			DownstreamBranch TEXT,
			UpstreamName     TEXT,
			UpstreamBranch   TEXT,
			Project          TEXT,
			Path             TEXT,
			OrderId          INTEGER NOT NULL,
			CommitSha1       TEXT,
			NetInsertions    INTEGER,
			NetDeletions     INTEGER,
			NumLinesInFile   INTEGER,
			TotalDeltas      INTEGER GENERATED ALWAYS AS (NetInsertions + NetDeletions) STORED,
			PRIMARY KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, Path, OrderId DESC, CommitSha1),
			FOREIGN KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1) REFERENCES BranchCommits(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1));

		CREATE INDEX IF NOT EXISTS branch_files_by_deltas ON BranchFiles(
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			TotalDeltas DESC,
			Path ASC
		);

		CREATE TABLE IF NOT EXISTS LatestBranchFiles(
			DownstreamName   TEXT,
			DownstreamBranch TEXT,
			UpstreamName     TEXT,
			UpstreamBranch   TEXT,
			Project          TEXT,
			Path             TEXT,
			OrderId          INTEGER NOT NULL,
			CommitSha1       TEXT,
			NetInsertions    INTEGER,
			NetDeletions     INTEGER,
			NumLinesInFile   INTEGER,
			TotalDeltas      INTEGER GENERATED ALWAYS AS (NetInsertions + NetDeletions) STORED,
			PRIMARY KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, Path),
			FOREIGN KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1) REFERENCES BranchCommits(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1));

		CREATE INDEX IF NOT EXISTS latest_branch_files_by_deltas ON LatestBranchFiles(
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			TotalDeltas DESC,
			Path ASC
		);

		CREATE TABLE IF NOT EXISTS Bugs(
			DownstreamName TEXT,
			Project        TEXT,
			CommitSha1     TEXT,
			Bug            INTEGER,
			PRIMARY KEY(DownstreamName, Project, CommitSha1, Bug),
			FOREIGN KEY(DownstreamName, Project, CommitSha1) REFERENCES Commits(DownstreamName, Project, CommitSha1));

		CREATE TABLE IF NOT EXISTS CRsFixed(
			DownstreamName TEXT,
			Project        TEXT,
			CommitSha1     TEXT,
			CRFixed        INTEGER,
			PRIMARY KEY(DownstreamName, Project, CommitSha1, CRFixed),
			FOREIGN KEY(DownstreamName, Project, CommitSha1) REFERENCES Commits(DownstreamName, Project, CommitSha1));

		CREATE TABLE IF NOT EXISTS ContentCommits(
			DownstreamName   TEXT,
			DownstreamBranch TEXT,
			UpstreamName     TEXT,
			UpstreamBranch   TEXT,
			Project          TEXT,
			CommitSha1       TEXT,
			MergeBase        TEXT,
			ResidentTime     INTEGER,
			OrderId          INTEGER,
			PRIMARY KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1));

		CREATE INDEX IF NOT EXISTS content_commits_by_order_id ON ContentCommits (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			OrderId DESC,
			CommitSha1
		);

		CREATE TABLE IF NOT EXISTS ContentHash(
			Hash          INTEGER,
			Content       TEXT,
			PRIMARY KEY(Hash));

		CREATE TABLE IF NOT EXISTS HunkSegments(
			DownstreamName TEXT,
			DownstreamBranch         TEXT,
			UpstreamName   TEXT,
			UpstreamBranch TEXT,
			Project        TEXT,
			CommitSha1     TEXT,
			Path           TEXT,
			Id             INTEGER,
			Start          INTEGER,
			Count          INTEGER,
			SegmentType    INTEGER,
			ContentHash    INTEGER,
			IsLive         INTEGER,
			DeadState      INTEGER,
			DeadSha1   	   TEXT,
			Successors     TEXT,
			PRIMARY KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1, Path, Id),
			FOREIGN KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1) REFERENCES ContentCommits(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project, CommitSha1),
			FOREIGN KEY(ContentHash) REFERENCES ContentHash(Hash));

		CREATE INDEX IF NOT EXISTS hunk_segments_by_path_id ON HunkSegments (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			Path,
			Id
		);

		CREATE INDEX IF NOT EXISTS hunk_segments_by_live_state ON HunkSegments (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			IsLive
		);

		CREATE INDEX IF NOT EXISTS hunk_segments_commit_pagination ON HunkSegments (
			DownstreamName,
			DownstreamBranch,
			UpstreamName,
			UpstreamBranch,
			Project,
			CommitSha1,
			Path,
			Start
		);`)

	return err
}

func (s *Sqlite) executeMostRecentCommitsQuery(ctx context.Context, projectConfig *types.ProjectConfig, table string) ([]types.ProjectCommit, error) {
	isFirstParentConstraint := ""
	if table == "BranchCommits" {
		isFirstParentConstraint = "C.IsFirstParent=1 AND"
	}

	queryMostRecentCommits, err := s.db.PrepareContext(ctx,
		fmt.Sprintf(`
		SELECT
			C.Project,
			C.CommitSha1
		FROM %s AS C
		JOIN (
			SELECT
				C.Project,
				MAX(C.OrderID) AS MaxID
			FROM %s AS C
			WHERE %s C.DownstreamName = ?1
			  AND C.DownstreamBranch = ?2
			  AND C.UpstreamName = ?3
			  AND C.UpstreamBranch = ?4
			GROUP BY
				C.Project
		) as InQ
		  ON C.Project = InQ.Project
		  AND C.OrderID = InQ.MaxID
		WHERE %s C.DownstreamName = ?1
		  AND C.DownstreamBranch = ?2
		  AND C.UpstreamName = ?3
		  AND C.UpstreamBranch = ?4;
		`, table, table, isFirstParentConstraint, isFirstParentConstraint))
	if err != nil {
		return nil, err
	}
	defer queryMostRecentCommits.Close()

	rows, err := queryMostRecentCommits.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projectCommits []types.ProjectCommit
	for rows.Next() {
		commit := types.ProjectCommit{}
		if err := rows.Scan(&commit.Project, &commit.Commit); err != nil {
			return nil, err
		}
		projectCommits = append(projectCommits, commit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return projectCommits, nil
}

// DownstreamUpstreamPairings returns all supported downstream/upstream pairings found in the store.
func (s *Sqlite) DownstreamUpstreamPairings(ctx context.Context) ([]storage.DownstreamUpstreamPairing, error) {
	query, err := s.db.PrepareContext(ctx, `
		SELECT DISTINCT DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch FROM BranchCommits
	`)
	if err != nil {
		return nil, err
	}
	defer query.Close()

	rows, err := query.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pairings []storage.DownstreamUpstreamPairing
	for rows.Next() {
		var pairing storage.DownstreamUpstreamPairing
		if err := rows.Scan(&pairing.DownstreamName, &pairing.DownstreamBranch, &pairing.UpstreamName, &pairing.UpstreamBranch); err != nil {
			return nil, err
		}
		pairings = append(pairings, pairing)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pairings, nil
}

// MostRecentStatsCommits returns the most recent commit matching provided
// name, downstream, and upstream for all projects for stat diffs.
func (s *Sqlite) MostRecentStatsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error) {
	return s.executeMostRecentCommitsQuery(ctx, projectConfig, "BranchCommits")
}

// MostRecentStatsCommit returns the most recent commit matching provided
// downstream, upstream, and project for stat diffs. Returns empty string if not found.
func (s *Sqlite) MostRecentStatsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error) {
	var sinceCommit types.SinceCommit

	queryMostRecentStatsCommit, err := s.db.PrepareContext(ctx,
		`
		SELECT CommitSha1, MergeBase, OrderId
		FROM BranchCommits
		WHERE DownstreamName = ?1
		  AND DownstreamBranch = ?2
		  AND UpstreamName = ?3
		  AND UpstreamBranch = ?4
		  AND Project = ?5
		  AND IsFirstParent = 1
		ORDER BY OrderId DESC
		LIMIT 1;
		`)
	if err != nil {
		return sinceCommit, err
	}
	defer queryMostRecentStatsCommit.Close()

	row := queryMostRecentStatsCommit.QueryRowContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name)
	if row.Err() != nil {
		return sinceCommit, row.Err()
	}

	// Intentional error ignore:
	// It is not an application error for there to be a missing row. This will
	// occur on bootstrap runs.
	err = row.Scan(&sinceCommit.Commit, &sinceCommit.MergeBase, &sinceCommit.OrderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sinceCommit, err
	}
	return sinceCommit, nil
}

// MostRecentFiles returns the most recent BranchFile objects found for the
// provided downstream, upstream, and project. Returns nil if not found.
func (s *Sqlite) MostRecentFiles(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]*stats.BranchFile, error) {
	queryMostRecentFileRows, err := s.db.PrepareContext(ctx,
		`
		SELECT
			LBF.Path,
			LBF.CommitSha1,
			LBF.NetInsertions,
			LBF.NetDeletions,
			LBF.NumLinesInFile,
			LBF.OrderId
		FROM LatestBranchFiles AS LBF
		WHERE LBF.DownstreamName = ?1
		  AND LBF.DownstreamBranch = ?2
		  AND LBF.UpstreamName = ?3
		  AND LBF.UpstreamBranch = ?4
		  AND LBF.Project = ?5;
		`)
	if err != nil {
		return nil, err
	}
	defer queryMostRecentFileRows.Close()
	rows, err := queryMostRecentFileRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mostRecentFiles := make(map[string]*stats.BranchFile)

	for rows.Next() {
		bf := &stats.BranchFile{}

		var maxID int64

		if err := rows.Scan(&bf.Path, &bf.CommitSha1, &bf.NetInsertions, &bf.NetDeletions, &bf.NumLinesInFile, &maxID); err != nil {
			return nil, err
		}
		mostRecentFiles[bf.Path] = bf
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return mostRecentFiles, nil
}

// CurrCommits returns the CommitSha1 strings of commits found for provided
// project.
func (s *Sqlite) CurrCommits(ctx context.Context, projectConfig *types.ProjectConfig) (map[string]bool, error) {
	currCommitsContext, err := s.db.PrepareContext(ctx,
		`
		SELECT CommitSha1
		FROM Commits
		WHERE DownstreamName = ?1
		  AND Project = ?2;
		`)
	if err != nil {
		return nil, err
	}
	defer currCommitsContext.Close()
	rows, err := currCommitsContext.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	currCommits := make(map[string]bool)
	for rows.Next() {
		var commit string
		if err := rows.Scan(&commit); err != nil {
			return nil, err
		}
		currCommits[commit] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return currCommits, nil
}

// AddStats inserts the StatObjects into the database.
func (s *Sqlite) AddStats(ctx context.Context, projectConfig *types.ProjectConfig, statObjects *stats.StatObjects, opts *storage.AddStatsOptions) error {
	if statObjects == nil {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	commitContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO Commits (
		DownstreamName,
		Project,
		CommitSha1,
		CommitType,
		Email,
		CommitTime,
		FirstParent,
		SecondParent,
		TreeHash,
		Subject,
		Body,
		ChangeID,
		FilesChanged,
		Insertions,
		Deletions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer commitContext.Close()
	commitTxContext := tx.StmtContext(ctx, commitContext)

	branchCommitContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO BranchCommits (
		DownstreamName,
		DownstreamBranch,
		UpstreamName,
		UpstreamBranch,
		Project,
		CommitSha1,
		IsFirstParent,
		MergeBase,
		OldestDescendant,
		ResidentTime,
		OrderId,
		NetFilesChanged,
		NetInsertions,
		NetDeletions)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer branchCommitContext.Close()
	branchCommitTxContext := tx.StmtContext(ctx, branchCommitContext)

	fileContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO Files (
		DownstreamName,
		Project,
		CommitSha1,
		Path,
		Insertions,
		Deletions)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer fileContext.Close()
	fileTxContext := tx.StmtContext(ctx, fileContext)

	branchFileContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO BranchFiles (
		DownstreamName,
		DownstreamBranch,
		UpstreamName,
		UpstreamBranch,
		Project,
		Path,
		OrderId,
		CommitSha1,
		NetInsertions,
		NetDeletions,
		NumLinesInFile)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer branchFileContext.Close()
	branchFileTxContext := tx.StmtContext(ctx, branchFileContext)

	latestBranchFileContext, err := s.db.PrepareContext(ctx,
		`
	INSERT OR REPLACE INTO LatestBranchFiles (
		DownstreamName,
		DownstreamBranch,
		UpstreamName,
		UpstreamBranch,
		Project,
		Path,
		OrderId,
		CommitSha1,
		NetInsertions,
		NetDeletions,
		NumLinesInFile)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer latestBranchFileContext.Close()
	latestBranchFileTxContext := tx.StmtContext(ctx, latestBranchFileContext)

	bugContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO Bugs (
		DownstreamName,
		Project,
		CommitSha1,
		Bug)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer bugContext.Close()
	bugTxContext := tx.StmtContext(ctx, bugContext)

	crFixedContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO CRsFixed (
		DownstreamName,
		Project,
		CommitSha1,
		CRFixed)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer crFixedContext.Close()
	crFixedTxContext := tx.StmtContext(ctx, crFixedContext)

	for _, commit := range statObjects.CommitObjects.Commits {
		if _, err := commitTxContext.ExecContext(
			ctx,
			projectConfig.DownstreamName,
			projectConfig.Name,
			commit.CommitSha1,
			commit.CommitType,
			commit.Email,
			commit.CommitTime,
			commit.FirstParent,
			commit.SecondParent,
			commit.TreeHash,
			commit.Subject,
			commit.Body,
			commit.ChangeID,
			commit.FilesChanged,
			commit.Insertions,
			commit.Deletions); err != nil {
			wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s : %w", projectConfig.DownstreamName, projectConfig.Name, commit.CommitSha1, err)
			if innerErr := tx.Rollback(); innerErr != nil {
				return fmt.Errorf("%v; %w", innerErr, wrappedErr)
			}

			return wrappedErr
		}
	}

	for _, branchCommit := range statObjects.CommitObjects.BranchCommits {
		if _, err := branchCommitTxContext.ExecContext(
			ctx,
			projectConfig.DownstreamName,
			projectConfig.DownstreamBranch,
			projectConfig.UpstreamName,
			projectConfig.UpstreamBranch,
			projectConfig.Name,
			branchCommit.CommitSha1,
			branchCommit.IsFirstParent,
			branchCommit.MergeBase,
			branchCommit.OldestDescendant,
			branchCommit.ResidentTime,
			branchCommit.OrderID,
			branchCommit.NetFilesChanged,
			branchCommit.NetInsertions,
			branchCommit.NetDeletions); err != nil {
			wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s, downstreamBranch: %s, upstreamName: %s, upstreamBranch: %s : %w",
				projectConfig.DownstreamName, projectConfig.Name, branchCommit.CommitSha1, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, err)
			if innerErr := tx.Rollback(); innerErr != nil {
				return fmt.Errorf("%v; %w", innerErr, wrappedErr)
			}

			return wrappedErr
		}
	}

	for _, files := range statObjects.FileObjects.Files {
		for _, file := range *files {
			if _, err := fileTxContext.ExecContext(
				ctx,
				projectConfig.DownstreamName,
				projectConfig.Name,
				file.CommitSha1,
				file.Path,
				file.Insertions,
				file.Deletions); err != nil {
				wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s, path: %s : %w", projectConfig.DownstreamName, projectConfig.Name, file.CommitSha1, file.Path, err)
				if innerErr := tx.Rollback(); innerErr != nil {
					return fmt.Errorf("%v; %w", innerErr, wrappedErr)
				}

				return wrappedErr
			}
		}
	}

	commitToOrderID := make(map[string]int64)
	var bcs []*stats.BranchCommit
	for _, bc := range statObjects.CommitObjects.BranchCommits {
		commitToOrderID[bc.CommitSha1] = bc.OrderID
		bcs = append(bcs, bc)
	}

	// Sort commits chronologically by OrderID
	slices.SortFunc(bcs, func(a, b *stats.BranchCommit) int {
		if a.OrderID < b.OrderID {
			return -1
		}
		if a.OrderID > b.OrderID {
			return 1
		}
		return 0
	})

	latestFiles := make(map[string]*stats.BranchFile)
	latestFileOrderIDs := make(map[string]int64)
	latestFileCommits := make(map[string]string)

	// Process writes sequentially in sorted chronological order
	for _, bc := range bcs {
		branchFiles := statObjects.FileObjects.BranchFiles[bc.CommitSha1]
		if branchFiles == nil {
			continue
		}
		orderID := bc.OrderID
		for _, branchFile := range *branchFiles {
			// Write to history table
			if _, err := branchFileTxContext.ExecContext(
				ctx,
				projectConfig.DownstreamName,
				projectConfig.DownstreamBranch,
				projectConfig.UpstreamName,
				projectConfig.UpstreamBranch,
				projectConfig.Name,
				branchFile.Path,
				orderID,
				branchFile.CommitSha1,
				branchFile.NetInsertions,
				branchFile.NetDeletions,
				branchFile.NumLinesInFile); err != nil {
				wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s, downstreamBranch: %s, upstreamName: %s, upstreamBranch: %s, path: %s : %w",
					projectConfig.DownstreamName, projectConfig.Name, branchFile.CommitSha1, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, branchFile.Path, err)
				if innerErr := tx.Rollback(); innerErr != nil {
					return fmt.Errorf("%v; %w", innerErr, wrappedErr)
				}
				return wrappedErr
			}

			// Collect/dedup latest file states in Go memory
			key := branchFile.Path
			if existingOrderID, ok := latestFileOrderIDs[key]; !ok || orderID > existingOrderID {
				latestFileOrderIDs[key] = orderID
				latestFiles[key] = branchFile
				latestFileCommits[key] = bc.CommitSha1
			}
		}
	}

	// Execute LatestBranchFiles writes for the deduped latest states
	for path, branchFile := range latestFiles {
		orderID := latestFileOrderIDs[path]
		commitSha1 := latestFileCommits[path]
		if _, err := latestBranchFileTxContext.ExecContext(
			ctx,
			projectConfig.DownstreamName,
			projectConfig.DownstreamBranch,
			projectConfig.UpstreamName,
			projectConfig.UpstreamBranch,
			projectConfig.Name,
			branchFile.Path,
			orderID,
			commitSha1,
			branchFile.NetInsertions,
			branchFile.NetDeletions,
			branchFile.NumLinesInFile); err != nil {
			wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s, downstreamBranch: %s, upstreamName: %s, upstreamBranch: %s, path: %s (latest) : %w",
				projectConfig.DownstreamName, projectConfig.Name, commitSha1, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, branchFile.Path, err)
			if innerErr := tx.Rollback(); innerErr != nil {
				return fmt.Errorf("%v; %w", innerErr, wrappedErr)
			}
			return wrappedErr
		}
	}

	for _, commit := range statObjects.CommitObjects.Commits {
		for _, bug := range commit.Bugs {
			if _, err := bugTxContext.ExecContext(
				ctx,
				projectConfig.DownstreamName,
				projectConfig.Name,
				commit.CommitSha1,
				bug); err != nil {
				wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s bug: %d : %w", projectConfig.DownstreamName, projectConfig.Name, commit.CommitSha1, bug, err)
				if innerErr := tx.Rollback(); innerErr != nil {
					return fmt.Errorf("%v; %w", innerErr, wrappedErr)
				}

				return wrappedErr
			}
		}
	}

	for _, commit := range statObjects.CommitObjects.Commits {
		for _, crFixed := range commit.CRsFixed {
			if _, err := crFixedTxContext.ExecContext(
				ctx,
				projectConfig.DownstreamName,
				projectConfig.Name,
				commit.CommitSha1,
				crFixed); err != nil {
				wrappedErr := fmt.Errorf("downstreamName: %s, project: %s, commit: %s crFixed: %d : %w", projectConfig.DownstreamName, projectConfig.Name, commit.CommitSha1, crFixed, err)
				if innerErr := tx.Rollback(); innerErr != nil {
					return fmt.Errorf("%v; %w", innerErr, wrappedErr)
				}

				return wrappedErr
			}
		}
	}

	if err := tx.Commit(); err != nil {
		if innerErr := tx.Rollback(); innerErr != nil {
			return fmt.Errorf("%v; %w", innerErr, err)
		}

		return err
	}

	return nil
}

// AllHashes returns all content hashes currently in the database.
func (s *Sqlite) AllHashes(ctx context.Context) (map[int64]bool, error) {
	queryAllHashRows, err := s.db.PrepareContext(ctx,
		`
		SELECT Hash FROM ContentHash;
		`)
	if err != nil {
		return nil, err
	}
	defer queryAllHashRows.Close()
	rows, err := queryAllHashRows.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	allHashes := make(map[int64]bool)

	for rows.Next() {
		var hash int64

		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		allHashes[hash] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return allHashes, nil
}

// MostRecentContentsCommits returns the most recent commit matching provided
// downstreamName, downstreamBranch, upstreamName, and upstreamBranch for all projects for content diffs.
func (s *Sqlite) MostRecentContentsCommits(ctx context.Context, projectConfig *types.ProjectConfig) ([]types.ProjectCommit, error) {
	return s.executeMostRecentCommitsQuery(ctx, projectConfig, "ContentCommits")
}

// MostRecentContentsCommit returns the most recent commit matching provided
// downstream, upstream, and project for contents diffs. Returns empty string if not found.
func (s *Sqlite) MostRecentContentsCommit(ctx context.Context, projectConfig *types.ProjectConfig) (types.SinceCommit, error) {
	var sinceCommit types.SinceCommit

	queryMostRecentContentsCommit, err := s.db.PrepareContext(ctx,
		`
		SELECT CommitSha1, MergeBase, OrderId
		FROM ContentCommits
		WHERE DownstreamName = ?1
		  AND DownstreamBranch = ?2
		  AND UpstreamName = ?3
		  AND UpstreamBranch = ?4
		  AND Project = ?5
		ORDER BY OrderId DESC
		LIMIT 1;
		`)
	if err != nil {
		return sinceCommit, err
	}
	defer queryMostRecentContentsCommit.Close()

	row := queryMostRecentContentsCommit.QueryRowContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name)
	if row.Err() != nil {
		return sinceCommit, row.Err()
	}
	// Intentional error ignore:
	// It is not an application error for there to be a missing row. This will
	// occur on bootstrap runs.
	_ = row.Scan(&sinceCommit.Commit, &sinceCommit.MergeBase, &sinceCommit.OrderID)
	return sinceCommit, nil
}

// MostRecentLiveHunks returns the most recent live hunks matching provided
// downstream, upstream, and project for contents diffs.
func (s *Sqlite) MostRecentLiveHunks(ctx context.Context, projectConfig *types.ProjectConfig) (contents.LiveHunkSegmentsData, error) {
	queryMostRecentLiveHunksRows, err := s.db.PrepareContext(ctx,
		`
		SELECT Id, Path, ContentHash, Start, Count, SegmentType, CommitSha1
		FROM HunkSegments
		WHERE DownstreamName = ?1
		  AND DownstreamBranch = ?3
		  AND UpstreamName = ?4
		  AND UpstreamBranch = ?5
		  AND Project = ?2
		  AND IsLive = 1;
		`)
	if err != nil {
		return nil, err
	}
	defer queryMostRecentLiveHunksRows.Close()
	rows, err := queryMostRecentLiveHunksRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.Name, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mostRecentLiveHunks := make(map[string]map[int64]map[uint64]*contents.UniqueDiff)

	for rows.Next() {
		var id int64
		var path string
		var hash int64
		var start int
		var count int
		var segmentType int
		var commitSha1 string

		if err := rows.Scan(&id, &path, &hash, &start, &count, &segmentType, &commitSha1); err != nil {
			return nil, err
		}

		hashUint := hash
		h := types.HunkInfo{
			Start:       start,
			Count:       count,
			SegmentType: constants.HunkSegmentType(segmentType),
		}

		uniqueDiff := &contents.UniqueDiff{
			ID:         id,
			CommitSha1: commitSha1,
			File:       path,
			HunkInfo:   h,
			Hash:       hashUint,
		}

		mHash, exists := mostRecentLiveHunks[path]
		if !exists {
			mHash = make(map[int64]map[uint64]*contents.UniqueDiff)
			mostRecentLiveHunks[path] = mHash
		}
		mHunk, exists := mHash[hashUint]
		if !exists {
			mHunk = make(map[uint64]*contents.UniqueDiff)
			mHash[hashUint] = mHunk
		}
		mHunk[h.ToUint64()] = uniqueDiff
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return mostRecentLiveHunks, nil
}

func unmarshalSuccessors(input string) ([]int64, error) {
	if input == "" {
		return nil, nil
	}

	var ids []int64
	if err := json.Unmarshal([]byte(input), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func marshalSuccessors(input []int64) (string, error) {
	if len(input) == 0 {
		return "", nil
	}

	b, err := json.Marshal(input)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// MostRecentDeadHunks returns the most recent dead hunks matching provided
// downstream, upstream, and project for contents diffs.
func (s *Sqlite) MostRecentDeadHunks(ctx context.Context, projectConfig *types.ProjectConfig) (contents.DeadHunkSegmentsData, error) {
	queryMostRecentDeadHunksRows, err := s.db.PrepareContext(ctx,
		`
		SELECT Id, Path, ContentHash, Start, Count, SegmentType, CommitSha1, DeadState, DeadSha1, Successors
		FROM HunkSegments
		WHERE DownstreamName = ?1
		  AND DownstreamBranch = ?3
		  AND UpstreamName = ?4
		  AND UpstreamBranch = ?5
		  AND Project = ?2
		  AND IsLive = 0;
		`)
	if err != nil {
		return nil, err
	}
	defer queryMostRecentDeadHunksRows.Close()
	rows, err := queryMostRecentDeadHunksRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.Name, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mostRecentDeadHunks := make(map[string]map[int64]map[uint64]map[string]*contents.DeadInfo)
	// Map UniqueDiff.ID to its successors' UniqueDiff.ID
	successorIDs := make(map[int64][]int64)
	maybeAppendSuccessors := func(id int64, successors string) error {
		if successors == "" {
			return nil
		}
		ids, err := unmarshalSuccessors(successors)
		if err != nil {
			return err
		}
		successorIDs[id] = ids

		return nil
	}

	for rows.Next() {
		var id int64
		var path string
		var hash int64
		var start int
		var count int
		var segmentType int
		var commitSha1 string
		var deadState contents.DeadInfoState
		var deadSha1 string
		var successors string

		if err := rows.Scan(&id, &path, &hash, &start, &count, &segmentType, &commitSha1, &deadState, &deadSha1, &successors); err != nil {
			return nil, err
		}

		hashUint := hash
		h := types.HunkInfo{
			Start:       start,
			Count:       count,
			SegmentType: constants.HunkSegmentType(segmentType),
		}

		mHash, exists := mostRecentDeadHunks[path]
		if !exists {
			mHash = make(map[int64]map[uint64]map[string]*contents.DeadInfo)
			mostRecentDeadHunks[path] = mHash
		}
		mHunk, exists := mHash[hashUint]
		if !exists {
			mHunk = make(map[uint64]map[string]*contents.DeadInfo)
			mHash[hashUint] = mHunk
		}
		mCommit, exists := mHunk[h.ToUint64()]
		if !exists {
			mCommit = make(map[string]*contents.DeadInfo)
			mHunk[h.ToUint64()] = mCommit
		}

		if _, exists = mCommit[commitSha1]; exists {
			return nil, fmt.Errorf("found an already existing deadInfo for %s", commitSha1)
		}

		uniqueDiff := &contents.UniqueDiff{
			ID:         id,
			CommitSha1: commitSha1,
			File:       path,
			HunkInfo:   h,
			Hash:       hashUint,
		}

		deadInfo := &contents.DeadInfo{
			State:      deadState,
			UniqueDiff: uniqueDiff,
			Successors: nil,
			DeadSha1:   deadSha1,
		}

		mCommit[commitSha1] = deadInfo
		if err := maybeAppendSuccessors(uniqueDiff.ID, successors); err != nil {
			return nil, err
		}
	}

	// Map UniqueDiff.ID to its UniqueDiff
	uniqueDiffs := make(map[int64]*contents.UniqueDiff)
	liveHunkSegments, err := s.MostRecentLiveHunks(ctx, projectConfig)
	if err != nil {
		return nil, err
	}

	for _, m1 := range liveHunkSegments {
		for _, m2 := range m1 {
			for _, uniqueDiff := range m2 {
				uniqueDiffs[uniqueDiff.ID] = uniqueDiff
			}
		}
	}

	for _, m1 := range mostRecentDeadHunks {
		for _, m2 := range m1 {
			for _, m3 := range m2 {
				for _, deadInfo := range m3 {
					found := successorIDs[deadInfo.UniqueDiff.ID]
					if found == nil {
						continue
					}
					deadInfo.Successors = make([]*contents.UniqueDiff, len(found))
					for idx, sID := range found {
						deadInfo.Successors[idx] = uniqueDiffs[sID]
					}
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return mostRecentDeadHunks, nil
}

// AddContentHashes inserts the hash contents to the database.
func (s *Sqlite) AddContentHashes(ctx context.Context, hashToContent map[int64][]string) error {
	if len(hashToContent) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	contentContext, err := s.db.PrepareContext(ctx,
		`
	INSERT OR IGNORE INTO ContentHash (
		Hash,
		Content)
		VALUES (?, ?)
	`)
	if err != nil {
		return err
	}
	defer contentContext.Close()
	contentTxContext := tx.StmtContext(ctx, contentContext)

	for hash, content := range hashToContent {
		joined := truncate.Join(content, "\n", maxContentSize)
		if _, err := contentTxContext.ExecContext(ctx, hash, joined); err != nil {
			infoErr := fmt.Errorf("hash: %d content: %v : %v", hash, content, err)
			if innerErr := tx.Rollback(); innerErr != nil {
				return fmt.Errorf("%v; %v", innerErr, infoErr)
			}

			return infoErr
		}
	}

	if err := tx.Commit(); err != nil {
		if innerErr := tx.Rollback(); innerErr != nil {
			return fmt.Errorf("%v; %w", innerErr, err)
		}

		return err
	}

	return nil
}

// SplitTransactions is an empty method for Sqlite.
func (s *Sqlite) SplitTransactions(ctx context.Context, projectConfig *types.ProjectConfig, action string) (*types.ResultsDescriptor, error) {
	return nil, nil
}

// AddDiffResults inserts the DiffResults into the database.
func (s *Sqlite) AddDiffResults(ctx context.Context, projectConfig *types.ProjectConfig, diffResults *contents.DiffResults, options *storage.AddDiffResultsOptions) error {
	if diffResults == nil {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	commitContext, err := s.db.PrepareContext(ctx,
		`
	INSERT INTO ContentCommits (
		DownstreamName,
		DownstreamBranch,
		UpstreamName,
		UpstreamBranch,
		Project,
		CommitSha1,
		MergeBase,
		ResidentTime,
		OrderId)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer commitContext.Close()
	commitTxContext := tx.StmtContext(ctx, commitContext)

	segmentContext, err := s.db.PrepareContext(ctx,
		`
	INSERT OR REPLACE INTO HunkSegments (
		DownstreamName,
		DownstreamBranch,
		UpstreamName,
		UpstreamBranch,
		Project,
		CommitSha1,
		Id,
		Path,
		Start,
		Count,
		SegmentType,
		ContentHash,
		IsLive,
		DeadState,
		DeadSha1,
		Successors)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer segmentContext.Close()
	segmentTxContext := tx.StmtContext(ctx, segmentContext)

	for _, contentCommit := range diffResults.Commits {
		if _, err := commitTxContext.ExecContext(
			ctx,
			projectConfig.DownstreamName,
			projectConfig.DownstreamBranch,
			projectConfig.UpstreamName,
			projectConfig.UpstreamBranch,
			projectConfig.Name,
			contentCommit.Commit,
			contentCommit.MergeBase,
			contentCommit.ResidentTime,
			contentCommit.OrderID); err != nil {
			wrappedErr := fmt.Errorf("commit: %s mergeBase: %s residentTime: %d orderID: %d : %w", contentCommit.Commit, contentCommit.MergeBase, contentCommit.ResidentTime, contentCommit.OrderID, err)
			if innerErr := tx.Rollback(); innerErr != nil {
				return fmt.Errorf("%v; %w", innerErr, wrappedErr)
			}

			return wrappedErr
		}
	}

	for file, m1 := range diffResults.LiveHunkSegments {
		for hash, m2 := range m1 {
			for h, diffID := range m2 {
				isLive := 1
				deadState := contents.DeadStateUnset
				deadSha1 := ""
				successors := ""
				hunkInfo := types.HunkInfo{}
				hunkInfo.FromUint64(h)
				if _, err := segmentTxContext.ExecContext(
					ctx,
					projectConfig.DownstreamName,
					projectConfig.DownstreamBranch,
					projectConfig.UpstreamName,
					projectConfig.UpstreamBranch,
					projectConfig.Name,
					diffID.CommitSha1,
					diffID.ID,
					file,
					hunkInfo.Start,
					hunkInfo.Count,
					hunkInfo.SegmentType,
					hash,
					isLive,
					deadState,
					deadSha1,
					successors); err != nil {
					wrappedErr := fmt.Errorf("downstreamName: %s project: %s commit: %s file: %s start: %d count: %d segmentType: %d ID: %d: %w",
						projectConfig.DownstreamName, projectConfig.Name, diffID.CommitSha1, file, hunkInfo.Start, hunkInfo.Count, hunkInfo.SegmentType, diffID.ID, err)
					if innerErr := tx.Rollback(); innerErr != nil {
						return fmt.Errorf("%v; %w", innerErr, wrappedErr)
					}

					return wrappedErr
				}
			}
		}
	}

	for file, m1 := range diffResults.DeadHunkSegments {
		for hash, m2 := range m1 {
			for h, m3 := range m2 {
				hunkInfo := types.HunkInfo{}
				hunkInfo.FromUint64(h)
				isLive := 0
				for commit, deadInfo := range m3 {
					successorIDs := make([]int64, len(deadInfo.Successors))
					for idx, s := range deadInfo.Successors {
						successorIDs[idx] = s.ID
					}
					successors, err := marshalSuccessors(successorIDs)
					if err != nil {
						return err
					}
					if _, err = segmentTxContext.ExecContext(
						ctx,
						projectConfig.DownstreamName,
						projectConfig.DownstreamBranch,
						projectConfig.UpstreamName,
						projectConfig.UpstreamBranch,
						projectConfig.Name,
						commit,
						deadInfo.UniqueDiff.ID,
						file,
						hunkInfo.Start,
						hunkInfo.Count,
						hunkInfo.SegmentType,
						hash,
						isLive,
						deadInfo.State,
						deadInfo.DeadSha1,
						successors); err != nil {
						wrappedErr := fmt.Errorf("downstreamName: %s project: %s commit: %s file: %s start: %d count: %d segmentType: %d ID: %d: %w",
							projectConfig.DownstreamName, projectConfig.Name, commit, file, hunkInfo.Start, hunkInfo.Count, hunkInfo.SegmentType, deadInfo.UniqueDiff.ID, err)
						if innerErr := tx.Rollback(); innerErr != nil {
							return fmt.Errorf("%v; %w", innerErr, wrappedErr)
						}

						return wrappedErr
					}
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		if innerErr := tx.Rollback(); innerErr != nil {
			return fmt.Errorf("%v; %w", innerErr, err)
		}

		return err
	}

	return nil
}

// ContentForHash returns the content matching hashed value.
func (s *Sqlite) ContentForHash(ctx context.Context, hash int64) (string, error) {
	var content string

	queryContentForHash, err := s.db.PrepareContext(ctx,
		`
		SELECT Content FROM ContentHash WHERE Hash=?1;
		`)
	if err != nil {
		return "", err
	}
	defer queryContentForHash.Close()

	row := queryContentForHash.QueryRowContext(ctx, hash)
	if row.Err() != nil {
		return "", row.Err()
	}
	if err = row.Scan(&content); err != nil {
		return "", nil
	}

	return content, nil
}

func singleProjectOrErr(applicationConfig *types.ApplicationConfig) (string, error) {
	if len(applicationConfig.Projects) != 1 {
		return "", fmt.Errorf("expected a single project but received %v", len(applicationConfig.Projects))
	}
	if applicationConfig.Projects[0].Name == "" {
		return "", errors.New("project is not set")
	}

	return applicationConfig.Projects[0].Name, nil
}

// HunkByID returns the hunk matching the ID.
func (s *Sqlite) HunkByID(ctx context.Context, queryConfig *types.QueryConfig) (*contents.HunkListItem, error) {
	id, err := strconv.ParseInt(queryConfig.HunkID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse applicationConfig.HunkID: %v", err)
	}

	query, err := s.db.PrepareContext(ctx,
		`
			SELECT
				H.Id,
				H.CommitSha1,
				H.Start,
				H.Count,
				H.SegmentType,
				H.ContentHash,
				H.IsLive,
				H.DeadState,
				H.DeadSha1,
				H.Successors,
				CC.MergeBase,
				CC.ResidentTime,
				CH.Content
			FROM HunkSegments AS H
			JOIN ContentCommits AS CC
			  ON H.DownstreamName = CC.DownstreamName
			  AND H.DownstreamBranch = CC.DownstreamBranch
			  AND H.UpstreamName = CC.UpstreamName
			  AND H.UpstreamBranch = CC.UpstreamBranch
			  AND H.Project = CC.Project
			  AND H.CommitSha1 = CC.CommitSha1
			JOIN ContentHash AS CH
			  ON H.ContentHash = CH.Hash
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND H.Path = ?6
			  AND H.Id = ?7;
			`)
	if err != nil {
		return nil, err
	}
	defer query.Close()

	appConfig := queryConfig.ApplicationConfig
	project, err := singleProjectOrErr(appConfig)
	if err != nil {
		return nil, err
	}
	row := query.QueryRowContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.File, id)
	if row.Err() != nil {
		return nil, row.Err()
	}

	item := &contents.HunkListItem{
		Path: queryConfig.File,
	}

	var segmentType constants.HunkSegmentType
	var isLive int
	var deadState contents.DeadInfoState
	var residentTime int
	var successors string
	if err := row.Scan(&item.ID, &item.CommitSha1, &item.Start, &item.Count, &segmentType, &item.ContentHash, &isLive, &deadState, &item.DeadSha1, &successors, &item.MergeBase, &residentTime, &item.Content); err != nil {
		return nil, err
	}
	item.SegmentType = constants.HunkSegmentTypeString(segmentType)
	item.IsLive = (isLive == 1)
	item.DeadState = contents.DeadInfoStateString(deadState)
	item.ResidentTime = time.Unix(int64(residentTime), 0).Format(time.UnixDate)
	item.Successors, err = unmarshalSuccessors(successors)
	if err != nil {
		return nil, err
	}

	return item, nil
}

// SortedFiles returns all files present in storage.
// Files for all commits are returned unless applicationConfig.Commit is
// a non-empty string.
func (s *Sqlite) SortedFiles(ctx context.Context, queryConfig *types.QueryConfig) ([]string, error) {
	var (
		query       string
		sortedFiles []string
		maxID       int64
	)

	appConfig := queryConfig.ApplicationConfig
	switch queryConfig.QueryMode() {
	case types.QueryModeCommit:
		query =
			`
			SELECT H.Path
			FROM HunkSegments AS H
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND H.CommitSha1 = ?6
			GROUP BY H.Path
			ORDER BY H.Path;
			`
	case types.QueryModeRange:
		var err error
		maxID, err = maxOrderIDBeforeCommit(ctx, s, queryConfig)
		if err != nil {
			return nil, err
		}

		query =
			`
			SELECT H.Path
			FROM HunkSegments AS H
			JOIN ContentCommits AS CC
			  ON H.DownstreamName = CC.DownstreamName
			  AND H.DownstreamBranch = CC.DownstreamBranch
			  AND H.UpstreamName = CC.UpstreamName
			  AND H.UpstreamBranch = CC.UpstreamBranch
			  AND H.Project = CC.Project
			  AND H.CommitSha1 = CC.CommitSha1
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND CC.OrderId <= ?6
			GROUP BY H.Path
			ORDER BY H.Path;
			`
	case types.QueryModeAll:
		query =
			`
			SELECT H.Path
			FROM HunkSegments AS H
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			GROUP BY H.Path
			ORDER BY H.Path;
			`
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	project, err := singleProjectOrErr(appConfig)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	switch queryConfig.QueryMode() {
	case types.QueryModeCommit:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.Commit)
	case types.QueryModeRange:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, maxID)
	case types.QueryModeAll:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var path string

		if err := rows.Scan(&path); err != nil {
			return nil, err
		}

		sortedFiles = append(sortedFiles, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sortedFiles, nil
}

// Commit returns the commit from storage.
func (s *Sqlite) Commit(ctx context.Context, queryConfig *types.QueryConfig) (*contents.CommitListItem, error) {
	query, err := s.db.PrepareContext(ctx,
		`
		SELECT CommitSha1, MergeBase, ResidentTime, OrderId
		FROM ContentCommits
		WHERE DownstreamName = ?1
		  AND DownstreamBranch = ?3
		  AND UpstreamName = ?4
		  AND UpstreamBranch = ?5
		  AND Project = ?2
		  AND CommitSha1 = ?6;
		`)
	if err != nil {
		return nil, err
	}
	defer query.Close()

	appConfig := queryConfig.ApplicationConfig
	project, err := singleProjectOrErr(appConfig)
	if err != nil {
		return nil, err
	}
	row := query.QueryRowContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.Commit)
	if row.Err() != nil {
		return nil, row.Err()
	}

	item := &contents.CommitListItem{}
	var residentTime int64
	if err := row.Scan(&item.CommitSha1, &item.MergeBase, &residentTime, &item.OrderID); err != nil {
		return nil, err
	}
	item.ResidentTime = time.Unix(int64(residentTime), 0).Format(time.UnixDate)

	return item, nil
}

// SortedCommits returns all commits present in storage.
func (s *Sqlite) SortedCommits(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.CommitListItem, error) {
	var (
		query         string
		sortedCommits []*contents.CommitListItem
		maxID         int64
	)

	appConfig := queryConfig.ApplicationConfig
	switch queryConfig.QueryMode() {
	case types.QueryModeRange:
		var err error
		maxID, err = maxOrderIDBeforeCommit(ctx, s, queryConfig)
		if err != nil {
			return nil, err
		}

		query =
			`
			SELECT CommitSha1, MergeBase, ResidentTime, OrderId
			FROM ContentCommits
			WHERE DownstreamName = ?1
			  AND DownstreamBranch = ?3
			  AND UpstreamName = ?4
			  AND UpstreamBranch = ?5
			  AND Project = ?2
			  AND OrderId <= ?6
			ORDER BY OrderId DESC;
			`
	case types.QueryModeAll, types.QueryModeCommit:
		query =
			`
			SELECT CommitSha1, MergeBase, ResidentTime, OrderId
			FROM ContentCommits
			WHERE DownstreamName = ?1
			  AND DownstreamBranch = ?3
			  AND UpstreamName = ?4
			  AND UpstreamBranch = ?5
			  AND Project = ?2
			ORDER BY OrderId DESC;
			`
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	project, err := singleProjectOrErr(appConfig)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	switch queryConfig.QueryMode() {
	case types.QueryModeRange:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, maxID)
	case types.QueryModeAll, types.QueryModeCommit:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch)
	}
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		item := &contents.CommitListItem{}

		var residentTime int64
		if err := rows.Scan(&item.CommitSha1, &item.MergeBase, &residentTime, &item.OrderID); err != nil {
			return nil, err
		}
		item.ResidentTime = time.Unix(int64(residentTime), 0).Format(time.UnixDate)

		sortedCommits = append(sortedCommits, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sortedCommits, nil
}

// SortedHunksForFile returns all Hunk Segments for a file.
// Hunks for all commits are returned unless appConfig.Commit is
// a non-empty string.
func (s *Sqlite) SortedHunksForFile(ctx context.Context, queryConfig *types.QueryConfig) ([]*contents.HunkListItem, error) {
	var (
		query       string
		sortedHunks []*contents.HunkListItem
		maxID       int64
	)

	appConfig := queryConfig.ApplicationConfig
	switch queryConfig.QueryMode() {
	case types.QueryModeCommit:
		query =
			`
			SELECT
				H.Id,
				H.CommitSha1,
				H.Start,
				H.Count,
				H.SegmentType,
				H.ContentHash,
				H.IsLive,
				H.DeadState,
				H.DeadSha1,
				H.Successors,
				CC.MergeBase,
				CC.ResidentTime,
				CH.Content
			FROM HunkSegments AS H
			JOIN ContentCommits AS CC
			  ON H.DownstreamName = CC.DownstreamName
			  AND H.DownstreamBranch = CC.DownstreamBranch
			  AND H.UpstreamName = CC.UpstreamName
			  AND H.UpstreamBranch = CC.UpstreamBranch
			  AND H.Project = CC.Project
			  AND H.CommitSha1 = CC.CommitSha1
			JOIN ContentHash AS CH
			  ON H.ContentHash = CH.Hash
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND H.Path = ?6
			  AND H.CommitSha1 = ?7
			ORDER BY H.Start;
			`
	case types.QueryModeRange:
		var err error
		maxID, err = maxOrderIDBeforeCommit(ctx, s, queryConfig)
		if err != nil {
			return nil, err
		}

		query =
			`
			SELECT
				H.Id,
				H.CommitSha1,
				H.Start,
				H.Count,
				H.SegmentType,
				H.ContentHash,
				H.IsLive,
				H.DeadState,
				H.DeadSha1,
				H.Successors,
				CC.MergeBase,
				CC.ResidentTime,
				CH.Content
			FROM HunkSegments AS H
			JOIN ContentCommits AS CC
			  ON H.DownstreamName = CC.DownstreamName
			  AND H.DownstreamBranch = CC.DownstreamBranch
			  AND H.UpstreamName = CC.UpstreamName
			  AND H.UpstreamBranch = CC.UpstreamBranch
			  AND H.Project = CC.Project
			  AND H.CommitSha1 = CC.CommitSha1
			JOIN ContentHash AS CH
			  ON H.ContentHash = CH.Hash
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND H.Path = ?6
			  AND CC.OrderId <= ?7
			ORDER BY CC.OrderId DESC, H.Start;
			`
	case types.QueryModeAll:
		query =
			`
			SELECT
				H.Id,
				H.CommitSha1,
				H.Start,
				H.Count,
				H.SegmentType,
				H.ContentHash,
				H.IsLive,
				H.DeadState,
				H.DeadSha1,
				H.Successors,
				CC.MergeBase,
				CC.ResidentTime,
				CH.Content
			FROM HunkSegments AS H
			JOIN ContentCommits AS CC
			  ON H.DownstreamName = CC.DownstreamName
			  AND H.DownstreamBranch = CC.DownstreamBranch
			  AND H.UpstreamName = CC.UpstreamName
			  AND H.UpstreamBranch = CC.UpstreamBranch
			  AND H.Project = CC.Project
			  AND H.CommitSha1 = CC.CommitSha1
			JOIN ContentHash AS CH
			  ON H.ContentHash = CH.Hash
			WHERE H.DownstreamName = ?1
			  AND H.DownstreamBranch = ?3
			  AND H.UpstreamName = ?4
			  AND H.UpstreamBranch = ?5
			  AND H.Project = ?2
			  AND H.Path = ?6
			ORDER BY CC.OrderId DESC, H.Start;
			`
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	project, err := singleProjectOrErr(appConfig)
	if err != nil {
		return nil, err
	}
	var rows *sql.Rows
	switch queryConfig.QueryMode() {
	case types.QueryModeCommit:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.File, queryConfig.Commit)
	case types.QueryModeRange:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.File, maxID)
	case types.QueryModeAll:
		rows, err = queryRows.QueryContext(ctx, appConfig.DownstreamName, project, appConfig.DownstreamBranch, appConfig.UpstreamName, appConfig.UpstreamBranch, queryConfig.File)
	}
	if err != nil {
		return nil, err
	}

	for rows.Next() {
		item := &contents.HunkListItem{
			Path: queryConfig.File,
		}

		var segmentType constants.HunkSegmentType
		var isLive int
		var deadState contents.DeadInfoState
		var residentTime int
		var successors string

		if err := rows.Scan(&item.ID, &item.CommitSha1, &item.Start, &item.Count, &segmentType, &item.ContentHash, &isLive, &deadState, &item.DeadSha1, &successors, &item.MergeBase, &residentTime, &item.Content); err != nil {
			return nil, err
		}
		item.SegmentType = constants.HunkSegmentTypeString(segmentType)
		item.IsLive = (isLive == 1)
		item.DeadState = contents.DeadInfoStateString(deadState)
		item.ResidentTime = time.Unix(int64(residentTime), 0).Format(time.UnixDate)
		item.Successors, err = unmarshalSuccessors(successors)
		if err != nil {
			return nil, err
		}

		sortedHunks = append(sortedHunks, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if queryConfig.QueryMode() == types.QueryModeRange {
		if err := resurrectHunkListItems(ctx, s, queryConfig, sortedHunks, maxID); err != nil {
			return sortedHunks, err
		}
	}

	return sortedHunks, nil
}

// HunksForFile returns all hunks for a file in a project.
func (s *Sqlite) HunksForFile(ctx context.Context, projectConfig *types.ProjectConfig, path string) ([]*contents.HunkListItem, error) {
	return s.SortedHunksForFile(ctx, &types.QueryConfig{
		ApplicationConfig: &types.ApplicationConfig{
			DownstreamName:   projectConfig.DownstreamName,
			DownstreamBranch: projectConfig.DownstreamBranch,
			UpstreamName:     projectConfig.UpstreamName,
			UpstreamBranch:   projectConfig.UpstreamBranch,
			Projects:         []types.NormalizedProjectConfig{{Name: projectConfig.Name}},
		},
		File: path,
	})
}

// HunksForCommit returns all hunks for a project at a specific commit.
func (s *Sqlite) HunksForCommit(ctx context.Context, projectConfig *types.ProjectConfig, commit string, opts *storage.HunksForCommitOptions) ([]*contents.HunkListItem, error) {
	limit := 100
	offset := 0
	if opts != nil {
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		if opts.Offset > 0 {
			offset = opts.Offset
		}
	}

	query := `
		SELECT
			H.Id,
			H.CommitSha1,
			H.Start,
			H.Count,
			H.SegmentType,
			H.ContentHash,
			H.IsLive,
			H.DeadState,
			H.DeadSha1,
			H.Successors,
			CC.MergeBase,
			CC.ResidentTime,
			CH.Content,
			H.Path
		FROM HunkSegments AS H
		JOIN ContentCommits AS CC
		  ON H.DownstreamName = CC.DownstreamName
		  AND H.DownstreamBranch = CC.DownstreamBranch
		  AND H.UpstreamName = CC.UpstreamName
		  AND H.UpstreamBranch = CC.UpstreamBranch
		  AND H.Project = CC.Project
		  AND H.CommitSha1 = CC.CommitSha1
		JOIN ContentHash AS CH
		  ON H.ContentHash = CH.Hash
		WHERE H.DownstreamName = ?1
		  AND H.DownstreamBranch = ?3
		  AND H.UpstreamName = ?4
		  AND H.UpstreamBranch = ?5
		  AND H.Project = ?2
		  AND H.CommitSha1 = ?6
		ORDER BY H.Path, H.Start
		LIMIT ?7 OFFSET ?8;
	`

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.Name, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, commit, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hunks []*contents.HunkListItem
	for rows.Next() {
		item := &contents.HunkListItem{}
		var segmentType constants.HunkSegmentType
		var isLive int
		var deadState contents.DeadInfoState
		var residentTime int
		var successors string

		if err := rows.Scan(&item.ID, &item.CommitSha1, &item.Start, &item.Count, &segmentType, &item.ContentHash, &isLive, &deadState, &item.DeadSha1, &successors, &item.MergeBase, &residentTime, &item.Content, &item.Path); err != nil {
			return nil, err
		}
		item.SegmentType = constants.HunkSegmentTypeString(segmentType)
		item.IsLive = (isLive == 1)
		item.DeadState = contents.DeadInfoStateString(deadState)
		item.ResidentTime = time.Unix(int64(residentTime), 0).Format(time.UnixDate)
		item.Successors, err = unmarshalSuccessors(successors)
		if err != nil {
			return nil, err
		}

		hunks = append(hunks, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return hunks, nil
}

// HunkChain returns the chain of hunks starting from a specific hunk ID.
func (s *Sqlite) HunkChain(ctx context.Context, projectConfig *types.ProjectConfig, hunkID string, path string) ([]*contents.HunkListItem, error) {
	var chain []*contents.HunkListItem
	currentID, err := strconv.ParseInt(hunkID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse hunkID: %v", err)
	}

	for {
		queryConfig := &types.QueryConfig{
			ApplicationConfig: &types.ApplicationConfig{
				DownstreamName:   projectConfig.DownstreamName,
				DownstreamBranch: projectConfig.DownstreamBranch,
				UpstreamName:     projectConfig.UpstreamName,
				UpstreamBranch:   projectConfig.UpstreamBranch,
				Projects:         []types.NormalizedProjectConfig{{Name: projectConfig.Name}},
			},
			File:   path,
			HunkID: strconv.FormatInt(currentID, 10),
		}

		item, err := s.HunkByID(ctx, queryConfig)
		if err != nil {
			return nil, err
		}
		if item == nil {
			break
		}

		chain = append(chain, item)

		if len(item.Successors) == 0 {
			break
		}

		currentID = item.Successors[0]
	}

	return chain, nil
}

// MostRecentProjectDeltas returns the project deltas at the latest analyzed commit.
// If projectConfig.Name is empty, all projects are returned. Otherwise, only the provided
// project is returned.
func (s *Sqlite) MostRecentProjectDeltas(ctx context.Context, projectConfig *types.ProjectConfig) ([]storage.ProjectDelta, error) {
	var query string
	var args []any

	if projectConfig.Name != "" {
		query = `
			SELECT
				BC.Project,
				BC.MergeBase,
				BC.CommitSha1,
				BC.NetInsertions,
				BC.NetDeletions,
				BC.NetFilesChanged
			FROM BranchCommits AS BC
			WHERE BC.DownstreamName = ?1
			  AND BC.DownstreamBranch = ?2
			  AND BC.UpstreamName = ?3
			  AND BC.UpstreamBranch = ?4
			  AND BC.Project = ?5
			  AND BC.IsFirstParent = True
			ORDER BY BC.OrderId DESC
			LIMIT 1
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name}
	} else {
		query = `
			SELECT
				BC.Project,
				BC.MergeBase,
				BC.CommitSha1,
				BC.NetInsertions,
				BC.NetDeletions,
				BC.NetFilesChanged
			FROM BranchCommits AS BC
			JOIN (
				SELECT
					BC.Project,
					MAX(BC.OrderId) AS MaxOrderId
				FROM BranchCommits AS BC
				WHERE BC.DownstreamName = ?1
				  AND BC.DownstreamBranch = ?2
				  AND BC.UpstreamName = ?3
				  AND BC.UpstreamBranch = ?4
				  AND BC.IsFirstParent = True
				GROUP BY BC.Project
			) As InQ
			  ON BC.Project = InQ.Project
			  AND BC.OrderId = InQ.MaxOrderId
			WHERE BC.DownstreamName = ?1
			  AND BC.DownstreamBranch = ?2
			  AND BC.UpstreamName = ?3
			  AND BC.UpstreamBranch = ?4
			  AND BC.IsFirstParent = True
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch}
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, args...)
	if err != nil {
		return nil, err
	}

	var deltas []storage.ProjectDelta
	for rows.Next() {
		delta := storage.ProjectDelta{}
		if err := rows.Scan(&delta.Project, &delta.MergeBase, &delta.CommitSha1, &delta.NetInsertions, &delta.NetDeletions, &delta.NetFilesChanged); err != nil {
			return nil, err
		}
		deltas = append(deltas, delta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return deltas, nil
}

// ProjectTimeSeries shows how a project changes over time.
func (s *Sqlite) ProjectTimeSeries(ctx context.Context, projectConfig *types.ProjectConfig) ([]storage.ProjectTimeSeries, error) {
	query := `
		SELECT
			BC.CommitSha1,
			BC.NetInsertions,
			BC.NetDeletions,
			BC.ResidentTime
		FROM BranchCommits AS BC
		WHERE BC.DownstreamName = ?1
		  AND BC.DownstreamBranch = ?2
		  AND BC.UpstreamName = ?3
		  AND BC.UpstreamBranch = ?4
		  AND BC.Project = ?5
		  AND BC.IsFirstParent = True
		ORDER BY BC.OrderId DESC
		`
	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name)
	if err != nil {
		return nil, err
	}

	var timeSeries []storage.ProjectTimeSeries
	for rows.Next() {
		ts := storage.ProjectTimeSeries{}
		if err := rows.Scan(&ts.CommitSha1, &ts.NetInsertions, &ts.NetDeletions, &ts.ResidentTime); err != nil {
			return nil, err
		}
		timeSeries = append(timeSeries, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return timeSeries, nil
}

// MostRecentFileDeltas returns the file deltas for a project at the latest analyzed commit.
// If path is empty, all files are returned. Otherwise, only the provided
// file is returned.
func (s *Sqlite) MostRecentFileDeltas(ctx context.Context, projectConfig *types.ProjectConfig, path string, opts *storage.FileDeltasOptions) ([]storage.FileDelta, error) {
	limit := 50
	offset := 0
	if opts != nil {
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		if opts.Offset > 0 {
			offset = opts.Offset
		}
	}

	var query string
	var args []any

	if path != "" {
		query = `
		SELECT
			LBF.Path,
			LBF.CommitSha1,
			BC.MergeBase,
			LBF.NetInsertions,
			LBF.NetDeletions,
			BC.ResidentTime
		FROM LatestBranchFiles AS LBF
		JOIN BranchCommits AS BC
		  ON LBF.DownstreamName = BC.DownstreamName
		  AND LBF.DownstreamBranch = BC.DownstreamBranch
		  AND LBF.UpstreamName = BC.UpstreamName
		  AND LBF.UpstreamBranch = BC.UpstreamBranch
		  AND LBF.Project = BC.Project
		  AND LBF.CommitSha1 = BC.CommitSha1
		WHERE BC.DownstreamName = ?1
		  AND BC.DownstreamBranch = ?2
		  AND BC.UpstreamName = ?3
		  AND BC.UpstreamBranch = ?4
		  AND BC.Project = ?5
		  AND LBF.Path = ?6
		  AND BC.IsFirstParent = True
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name, path}
	} else {
		query = `
		SELECT
			LBF.Path,
			LBF.CommitSha1,
			BC.MergeBase,
			LBF.NetInsertions,
			LBF.NetDeletions,
			BC.ResidentTime
		FROM LatestBranchFiles AS LBF
		JOIN BranchCommits AS BC
		  ON LBF.DownstreamName = BC.DownstreamName
		  AND LBF.DownstreamBranch = BC.DownstreamBranch
		  AND LBF.UpstreamName = BC.UpstreamName
		  AND LBF.UpstreamBranch = BC.UpstreamBranch
		  AND LBF.Project = BC.Project
		  AND LBF.CommitSha1 = BC.CommitSha1
		WHERE BC.IsFirstParent = True
		  AND BC.DownstreamName = ?1
		  AND BC.DownstreamBranch = ?2
		  AND BC.UpstreamName = ?3
		  AND BC.UpstreamBranch = ?4
		  AND BC.Project = ?5
		ORDER BY LBF.TotalDeltas DESC, LBF.Path ASC LIMIT ?6 OFFSET ?7
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name, limit, offset}
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, args...)
	if err != nil {
		return nil, err
	}

	var fileDeltas []storage.FileDelta
	for rows.Next() {
		delta := storage.FileDelta{}
		if err := rows.Scan(&delta.Path, &delta.CommitSha1, &delta.MergeBase, &delta.NetInsertions, &delta.NetDeletions, &delta.ResidentTime); err != nil {
			return nil, err
		}
		fileDeltas = append(fileDeltas, delta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return fileDeltas, nil
}

// CommitBugs shows the bugs associated with commits.
func (s *Sqlite) CommitBugs(ctx context.Context, projectConfig *types.ProjectConfig) (storage.CommitBugs, error) {
	query := `
		SELECT
			b.CommitSha1,
			b.Bug
		FROM Bugs AS b
		JOIN Commits AS c
		  ON b.DownstreamName = c.DownstreamName
		  AND b.Project = c.Project
		  AND b.CommitSha1 = c.CommitSha1
		WHERE c.DownstreamName = ?1
		  AND c.Project = ?2
	`

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.Name)
	if err != nil {
		return nil, err
	}

	bugs := make(storage.CommitBugs)
	for rows.Next() {
		var commit string
		var bug int
		if err := rows.Scan(&commit, &bug); err != nil {
			return nil, err
		}
		bugs[commit] = append(bugs[commit], bug)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return bugs, nil
}

// CommitDeltas returns branch commit level statistics.
// If commit is empty, all commits are returned. Otherwise, only the provided
// commit is returned.
func (s *Sqlite) CommitDeltas(ctx context.Context, projectConfig *types.ProjectConfig, commit string, opts *storage.CommitDeltasOptions) ([]storage.CommitDelta, error) {
	limit := 50
	offset := 0
	if opts != nil {
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		if opts.Offset > 0 {
			offset = opts.Offset
		}
	}

	var query string
	var args []any

	if commit != "" {
		query = `
			SELECT
				BC.CommitSha1,
				ChangeID,
				CommitType,
				Subject,
				Email,
				CommitTime,
				FilesChanged,
				Insertions,
				Deletions,
				IsFirstParent,
				MergeBase,
				OldestDescendant,
				ResidentTime,
				NetFilesChanged,
				NetInsertions,
				NetDeletions
			FROM BranchCommits AS BC
			JOIN Commits AS C
			  ON BC.DownstreamName = C.DownstreamName
			  AND BC.Project = C.Project
			  AND BC.CommitSha1 = C.CommitSha1
			WHERE BC.DownstreamName = ?1
			  AND BC.DownstreamBranch = ?2
			  AND BC.UpstreamName = ?3
			  AND BC.UpstreamBranch = ?4
			  AND BC.Project = ?5
			  AND BC.CommitSha1 = ?6
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name, commit}
	} else {
		query = `
			SELECT
				BC.CommitSha1,
				ChangeID,
				CommitType,
				Subject,
				Email,
				CommitTime,
				FilesChanged,
				Insertions,
				Deletions,
				IsFirstParent,
				MergeBase,
				OldestDescendant,
				ResidentTime,
				NetFilesChanged,
				NetInsertions,
				NetDeletions
			FROM BranchCommits AS BC
			JOIN Commits AS C
			  ON BC.DownstreamName = C.DownstreamName
			  AND BC.Project = C.Project
			  AND BC.CommitSha1 = C.CommitSha1
			WHERE BC.DownstreamName = ?1
			  AND BC.DownstreamBranch = ?2
			  AND BC.UpstreamName = ?3
			  AND BC.UpstreamBranch = ?4
			  AND BC.Project = ?5
			ORDER BY BC.TotalDeltas DESC, BC.CommitSha1 ASC
			LIMIT ?6 OFFSET ?7
		`
		args = []any{projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name, limit, offset}
	}

	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, args...)
	if err != nil {
		return nil, err
	}

	var commitDeltas []storage.CommitDelta
	for rows.Next() {
		commitDelta := storage.CommitDelta{}
		var commitType int64

		if err := rows.Scan(&commitDelta.CommitSha1,
			&commitDelta.ChangeID, &commitType, &commitDelta.Subject, &commitDelta.Email,
			&commitDelta.CommitTime, &commitDelta.FilesChanged, &commitDelta.Insertions, &commitDelta.Deletions,
			&commitDelta.IsFirstParent, &commitDelta.MergeBase, &commitDelta.OldestDescendant, &commitDelta.ResidentTime,
			&commitDelta.NetFilesChanged, &commitDelta.NetInsertions, &commitDelta.NetDeletions); err != nil {
			return nil, err
		}
		commitDelta.CommitType = constants.CommitTypeString(constants.CommitType(commitType))
		commitDeltas = append(commitDeltas, commitDelta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return commitDeltas, nil
}

// CommitFileDeltas returns branch file level statistics.
func (s *Sqlite) CommitFileDeltas(ctx context.Context, projectConfig *types.ProjectConfig, opts *storage.CommitFileDeltasOptions) ([]storage.CommitFileDelta, error) {
	limit := 50
	offset := 0
	if opts != nil {
		if opts.Limit > 0 {
			limit = opts.Limit
		}
		if opts.Offset > 0 {
			offset = opts.Offset
		}
	}

	query := `
		SELECT
			BFT.CommitSha1,
			CommitType,
			Subject,
			Email,
			CommitTime,
			CT.FilesChanged as CommitFilesChanged,
			CT.Insertions AS CommitInsertions,
			CT.Deletions AS CommitDeletions,
			IsFirstParent,
			MergeBase,
			OldestDescendant,
			ResidentTime,
			BCT.NetFilesChanged AS CommitNetFilesChanged,
			BCT.NetInsertions AS CommitNetInsertions,
			BCT.NetDeletions AS CommitNetDeletions,
			BFT.Path,
			BFT.NetInsertions AS FileNetInsertions,
			BFT.NetDeletions AS FileNetDeletions
		FROM BranchFiles AS BFT
		JOIN BranchCommits AS BCT
		  ON BFT.DownstreamName = BCT.DownstreamName
		  AND BFT.DownstreamBranch = BCT.DownstreamBranch
		  AND BFT.UpstreamName = BCT.UpstreamName
		  AND BFT.UpstreamBranch = BCT.UpstreamBranch
		  AND BFT.Project = BCT.Project
		  AND BFT.CommitSha1 = BCT.CommitSha1
		JOIN Commits AS CT
		  ON BFT.DownstreamName = CT.DownstreamName
		  AND BFT.Project = CT.Project
		  AND BFT.CommitSha1 = CT.CommitSha1
		WHERE BFT.DownstreamName = ?1
		  AND BFT.DownstreamBranch = ?2
		  AND BFT.UpstreamName = ?3
		  AND BFT.UpstreamBranch = ?4
		  AND BFT.Project = ?5
		ORDER BY BFT.TotalDeltas DESC, BFT.Path ASC
		LIMIT ?6 OFFSET ?7
	`
	queryRows, err := s.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows, err := queryRows.QueryContext(ctx, projectConfig.DownstreamName, projectConfig.DownstreamBranch, projectConfig.UpstreamName, projectConfig.UpstreamBranch, projectConfig.Name, limit, offset)
	if err != nil {
		return nil, err
	}

	var commitFileDeltas []storage.CommitFileDelta
	for rows.Next() {
		commitFileDelta := storage.CommitFileDelta{}

		if err := rows.Scan(&commitFileDelta.CommitSha1, &commitFileDelta.CommitType, &commitFileDelta.Subject,
			&commitFileDelta.Email, &commitFileDelta.CommitTime, &commitFileDelta.CommitFilesChanged,
			&commitFileDelta.CommitInsertions, &commitFileDelta.CommitDeletions, &commitFileDelta.IsFirstParent,
			&commitFileDelta.MergeBase, &commitFileDelta.OldestDescendant, &commitFileDelta.ResidentTime,
			&commitFileDelta.CommitNetFilesChanged, &commitFileDelta.CommitNetInsertions, &commitFileDelta.CommitNetDeletions,
			&commitFileDelta.Path, &commitFileDelta.FileNetInsertions, &commitFileDelta.FileNetDeletions); err != nil {
			return nil, err
		}
		commitFileDeltas = append(commitFileDeltas, commitFileDelta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return commitFileDeltas, nil
}

// Close closes the database.
func (s *Sqlite) Close() error {
	return s.db.Close()
}
