# Chisel Gemini Guide

## Overview
Chisel is a tool for analyzing Android source code forks. It helps developers understand how a downstream fork has diverged from its upstream source.

Key capabilities:

- Tracking Lines of Code (LoC) changes (additions/deletions).
- Visualizing file modifications and commit content changes.
- Navigating the successor DAG of diff hunks across history.
- An interactive CLI for walking through successor chains.

## Architectural Summary
- **Language**: Go
- **Primary Data Structures**: Defined in `types/types.go`, `stats/types.go`, and `contents/types.go`.
- **Storage Abstraction**: The `repositories/` package provides implementations for `SQLite` and a `Proxy` client.
- **MCP Server**: The `mcp/` package implements a Model Context Protocol server, enabling AI agents to query Chisel's data directly.

## Database Schema
Chisel's data model is defined in SQL tables. Key tables include:

- **`Commits`**: General commit metadata (SHA, type, author, time, stats).
- **`BranchCommits`**: Branch-specific analysis metadata (upstream/downstream refs, merge-base, resident time).
- **`Files` & `BranchFiles`**: File-level diff statistics and LoC counts.
- **`HunkSegments`**: Detailed diff hunk data, including their state (Live, Retired, Succeeded) and a list of successor hunk IDs. This table is central to Chisel's ability to track changes across time and renames.
- **`ContentHash`**: A deduplicated storage table for hunk contents.
- **`Bugs` & `CRsFixed`**: Links between commits and issue tracking IDs.

## Model Context Protocol (MCP) Server

Chisel provides an MCP server to allow AI agents to query analyzed fork data
directly.

### Design Principles

-   **Envelope Pattern**: All tool responses include the input parameters to
    help LLMs maintain context and prevent cross-pollination in long
    conversations.
-   **Data Representation**: Responses are currently returned as row-based JSON
    objects. Future optimizations may include Markdown tables for token
    efficiency and field masking.
-   **Pagination**: Uses Offset-based Pagination (`limit` and `offset`). Stable
    sorting is required and enforced by appending a unique identifier to every
    sort clause.

### MCP Methods

-   `MostRecentStatsCommits`: Latest analyzed commit for all projects in a
    branch pair.
-   `MostRecentStatsCommit`: Metadata and analysis state for a single project's
    most recent commit.
-   `MostRecentProjectDeltas`: High-level LoC changes for projects.
-   `ProjectTimeSeries`: Track project statistics over time.
-   `MostRecentFileDeltas`: File-level changes for a specific project.
-   `CommitBugs`: Links commits to issue tracker IDs.
-   `CommitDeltas`: Detailed statistics at the individual commit level.
-   `CommitFileDeltas`: Commit-level changes broken down by file.
-   `HunksForCommit`: Detailed diff hunks for a specific project and commit
    (supports offset-based pagination).
-   `HunksForFile`: All historical diff hunks for a specific file path in a
    project.
-   `HunkChain`: Navigates the successor chain of a diff hunk starting from a
    specific ID.
-   `RegisterStorage`: Dynamically registers a storage backend configuration
    (SQLite or Proxy).

## Key Design Decisions

To optimize query performance and support the specific needs of AI agents and
developers, several key design choices were made regarding the database schema
and query implementation.

### 1. Denormalization of `OrderId` in `BranchFiles`

-   **Decision**: `OrderId` was added directly to the `BranchFiles` table,
    duplicating data that also exists in `BranchCommits`.
-   **Rationale**: Previously, finding the most recent version of a file
    required joining `BranchFiles` with `BranchCommits` just to read the
    `OrderId` and find the maximum one. Denormalizing `OrderId` allows these
    subqueries to be **join-free**, significantly reducing query overhead.

### 2. `BranchFiles` Contains Only First-Parent Commits

-   **Decision**: The application logic enforces that records are only inserted
    into `BranchFiles` for commits that are on the first-parent path (mainline
    history).
-   **Rationale**: This application-level invariant allows queries on
    `BranchFiles` to assume all records are relevant for mainline history
    without needing to join with `BranchCommits` to check `IsFirstParent =
    True`. This further eliminates the need for joins in heavy queries like
    `MostRecentFileDeltas`.

### 3. Primary Key Ordering on `BranchFiles`

-   **Decision**: The primary key for `BranchFiles` is ordered with `Path`
    before `OrderId DESC` and `CommitSha1`.
-   **Rationale**:
    -   Putting `Path` early enables efficient filtering and grouping by file
        path.
    -   Putting `OrderId DESC` before `CommitSha1` ensures that all versions of
        a file are clustered together, with the **most recent version appearing
        first**. This allows the database to find the latest state of a file via
        a fast index lookup rather than a full scan and aggregation.

### 4. Stored Generated Column for `TotalDeltas`

-   **Decision**: A stored generated column `TotalDeltas` (sum of insertions and
    deletions) was added to both `BranchCommits` and `BranchFiles`, with
    corresponding indexes.
-   **Rationale**: Sorting by the sum of insertions and deletions is a common
    operation. Without a stored column, the database must calculate the sum at
    runtime for all candidate rows and then sort them, which scales poorly. The
    stored column combined with an index allows for instant sorted retrieval.

### 5. Dedicated Snapshot Table for Current File States (`LatestBranchFiles`)

-   **Decision**: Maintained a separate `LatestBranchFiles` snapshot table
    containing strictly one row per unique file path under `PRIMARY
    KEY(DownstreamName, DownstreamBranch, UpstreamName, UpstreamBranch, Project,
    Path)`.
-   **Rationale**: Previously, finding the latest file tree required a heavy
    self-join subquery over the entire modification history in `BranchFiles`
    using `MAX(OrderId)`. Storing only the current tip revision of each path in
    a dedicated table reduces O(N) history-scanning joins and application-side
    filtering fallbacks into flat, contiguous index seeks that complete
    instantly.

### 6. In-Memory Sequential Chronological Batch Writing

-   **Decision**: Enforced strict chronological sorting of commit batches by
    `OrderID` using Go's `slices` package before executing DB statement loops,
    combined with in-memory deduplication in Go map structures during writes in
    SQLite repositories.
-   **Rationale**: Bulk importing commits in an unordered Go map traversal loop
    can cause older commits to blindly overwrite newer head-state entries in the
    `LatestBranchFiles` snapshot table. Memory deduping guarantees exactly one
    final head-state mutation is emitted per unique path, ensuring chronological
    execution correctness within batch transactions.

### 7. On-the-Fly Hunk History Sorting (Pragmatic Trade-off)

-   **Decision**: Decided *against* denormalizing `OrderId` into the
    `HunkSegments` table, keeping file hunk history sorting
    (`SortedHunksForFile`) as a runtime SQL join and sort on the fly.
-   **Rationale**: While the total commit history is immense, the historical
    modification footprint of a single file is naturally bounded (average file
    is modified in under 50 commits, legacy "god-classes" rarely exceed 1,000).
    Sorting 1,000 rows in memory takes under 10 microseconds. Since the query
    uses a highly selective index seek on `Path` first, the database in-memory
    join and sort is negligible. Denormalizing would introduce write overhead
    and table size growth for zero perceptible user-end lag reduction.

## Search Performance

Based on the SQLite schema in `repositories/sqlite.go` and recent performance improvements, here is
a guide to efficient searching in Chisel.

### Fast Searches (Indexed)

These searches and sorts are optimized and perform quickly when appropriate
downstream, upstream, project, and path filters are applied:

-   **Searching by specific commit**: Uses Primary Key on `Commits` and
    `BranchCommits`.
-   **Sorting all commits by `OrderId`**: Uses the `first_parent_max_order_id`
    index.
-   **Sorting all commits by `TotalDeltas`**: Uses the
    `branch_commits_by_deltas` index.
-   **Searching by specific file**: Uses Primary Key on `Files`, `BranchFiles`,
    and `LatestBranchFiles`.
-   **Querying current active file trees**: Performs a flat, contiguous seek on
    the `LatestBranchFiles` primary key (pre-filtered tip snapshot, bypassing
    database self-joins and memory fallbacks).
-   **Sorting/Paginating file changes by deltas**: Uses the
    `latest_branch_files_by_deltas` index for flat, pre-sorted scans on the
    snapshot table (instant O(1) sorted pagination, no history self-joins and no
    deep table scans).
-   **Sorting per-file by `OrderId`**: Uses the Primary Key on `BranchFiles`
    (which is ordered by `OrderId DESC`).
-   **Sorting all files by `TotalDeltas`**: Uses the `branch_files_by_deltas`
    index.
-   **Querying/Paginating hunk segments by commit**: Uses the secondary index
    `hunk_segments_commit_pagination` `(Downstream..., Project, CommitSha1,
    Path, Start)` for pre-sorted O(1) paginated range seeks (streams contiguous
    file hunks sequentially by line numbers without database-side memory
    sorting).
-   **Querying hunk segments by file path (across commits)**: Fast range seek on
    the secondary index `hunk_segments_by_path_id` `(Downstream..., Project,
    Path, Id)` (bypasses commit SHA matching to scan history).
-   **Point lookups for specific hunks by ID**: Fast point lookup on the
    secondary index `hunk_segments_by_path_id` (fully matching the entire prefix
    key, including `Path` and `Id`).

### Slow Searches (Table Scans)

Searches that do not use indexes or primary key prefixes will perform slowly and
likely result in table scans. Avoid these or use them with caution:

-   Searching for commits or files without specifying `DownstreamName` and
    `Project` (and `DownstreamBranch`, `UpstreamName`, `UpstreamBranch` for
    branch-specific tables).
-   Searching `Commits` by fields like `Email`, `Subject`, or `Body` without
    filtering by Primary Key first.
-   Searching `Files` or `BranchFiles` by `Insertions` or `Deletions` without
    using the `TotalDeltas` index or filtering by path.
-   **Sorting hunk segments for a file by `OrderId`**: (Pragmatically Fast)
    Because `OrderId` resides only in `ContentCommits` and not `HunkSegments`,
    this requires a `JOIN` and a runtime database memory sort. However, because
    the historical footprint of a single file is naturally tiny (typically
    <50 rows, rarely >1,000), the memory sort operates on a very small dataset
    and is exceptionally fast (<10 microseconds in Go), making further schema
    denormalization a premature optimization.
-   Arbitrary sorts on large datasets that are not backed by pre-defined
    indexes.

## Key Subdirectories
- `commands/`: Implementation of CLI subcommands (`init`, `stats`, `contents`, `list`, `interactive`, etc.).
- `repositories/`: Database interaction logic and storage implementations for different backends.
- `stats/`: Logic for collecting high-level diff statistics (LoC, file changes).
- `contents/`: Logic for collecting and navigating detailed content diffs and hunk successor chains.
- `git/`, `repositories/`: Git and repo tool wrappers.

## Guidance for AI Agents

### Understanding the Data
- Use the MCP server (if available) for querying analyzed data (see `mcp/mcp.go`).

### Development & Testing
- **Building**: `go build chisel.go`
- **Testing**: Run tests with `go test ./...`.
- **Adding Features**:
  - New CLI commands should be added as files in `commands/`.
  - New storage backends or queries should be added to `repositories/`.
  - Core analysis logic resides in `stats/` and `contents/`.

### Context Awareness
The tool heavily uses "context awareness" to infer configuration from the current working directory. It automatically infers `host`, `project`, `upstream`, and `downstream` based on the user's current `repo` or `git` environment.

