# Chisel

Chisel is a standalone code analysis and fork visualization tool designed to
inspect codebase modifications and track divergence between downstream Git
branches and upstream platform releases (such as AOSP). It calculates and
visualizes key divergence metrics, including Lines of Code (LoC) additions and
deletions, modified files, commit diffs, and the successor DAG of diff hunks
across history.

## Key Capabilities

- **LoC Tracking**: Detailed metrics for additions and deletions across commit
  history.
- **Successor Navigation**: Trace the evolution of specific code hunks through
  renames, merges, and refactors.
- **MCP Integration**: A built-in Model Context Protocol (MCP) server allows AI
  agents to query fork analysis data directly.
- **Interactive CLI**: Tools for walking through successor chains and analyzing
  commit deltas locally against a SQLite database.

## Getting Started

### Prerequisites

- Go 1.22+ (with CGO enabled for `github.com/mattn/go-sqlite3`)
- Git

### Building

```bash
go build chisel.go
```

### Configuration

Create a `chisel.yaml` configuration file specifying the upstream and downstream
branches and the projects to analyze:

```yaml
downstreamName: aosp-downstream
downstreamHost: https://android.googlesource.com
downstreamRemote: origin
downstreamBranch: main
upstreamName: aosp
upstreamHost: https://android.googlesource.com
upstreamRemote: origin
upstreamBranch: upstream-main
workDir: ./workdir
storageType: sqlite
projects:
  - name: platform/external/crosvm
    path: external/crosvm
maxProjectWorkers: 8
maxWorkersPerProject: 8
```

### Usage

Initialize the workarea and collect branch statistics and content diffs:

```bash
./chisel -c chisel.yaml init
./chisel -c chisel.yaml stats
./chisel -c chisel.yaml contents
```

Start the MCP server over stdio:

```bash
./chisel --storage_type=sqlite --work_dir=./workdir mcp
```

For detailed architectural documentation, database schema, and MCP method
specifications, see [GEMINI.md](GEMINI.md).

## Directory Structure

- `commands/`: CLI subcommand implementations (`init`, `stats`, `contents`,
  `list`, `interactive`, `mcp`, `serve`, `license`, `version`).
- `mcp/`: Model Context Protocol server implementation.
- `repositories/`: Database storage layer (`sqlite`, `proxy`).
- `stats/`: High-level diff statistics collection.
- `contents/`: Detailed hunk-level diff and successor tracking.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for instructions on how to contribute
and sign the Google Contributor License Agreement (CLA).

## License

Chisel is licensed under the [Apache License, Version 2.0](LICENSE).

## Security

See [SECURITY.md](SECURITY.md) for instructions on reporting security
vulnerabilities.

Eligibility for the [Google Open Source Software Vulnerability Rewards Program](https://bughunters.google.com/open-source-security) is determined by the [Google Open Source Software Vulnerability Reward Program Rules](https://bughunters.google.com/about/rules/open-source/google-open-source-software-vulnerability-reward-program-rules).
