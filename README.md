# Elephant

Elephant keeps local project state so a new coding agent can continue where the previous one stopped.

It stores facts, decisions, tasks, and explicit file relationships in one `elephant.zova` database. Git identity and commit metadata give those entries project context. Agents use an MCP stdio server; people can inspect the same state through the CLI.

## Build

Requirements: Go 1.26.5 or newer, Git, a C compiler, and the native Zova C ABI matching the pinned Go binding (`v1.0.0-rc.2`). The Go binding does not download or build the native library.

Obtain the matching native library from [Zova](https://github.com/ata-sesli/zova), or build that version with `zig build c-abi` in its source checkout. Then, from the Elephant directory:

```sh
export CGO_CFLAGS="-I/path/to/zova/include"
export CGO_LDFLAGS="-L/path/to/zova/zig-out/lib"
go build -o bin/elephant ./cmd/elephant
./bin/elephant --version
```

For an installed C ABI archive, point these flags at its `include` and `lib` directories instead. The binding already supplies `-lzova_c`. Keep the native header and library from the same Zova version.

## CI and GitHub releases

[The GitHub Actions workflow](.github/workflows/ci.yml) tests every branch push and pull request on exactly these four native runners:

| Platform | Runner | Release archive |
| --- | --- | --- |
| Linux ARM64 | `ubuntu-24.04-arm` | `elephant-vVERSION-linux-arm64.tar.gz` |
| Linux AMD64 | `ubuntu-24.04` | `elephant-vVERSION-linux-amd64.tar.gz` |
| Windows AMD64 | `windows-2022` | `elephant-vVERSION-windows-amd64.zip` |
| macOS ARM64 | `macos-15` | `elephant-vVERSION-macos-arm64.tar.gz` |

Each job checks formatting, verifies Go dependencies, builds Zova's native C ABI from the pinned `v1.0.0-rc.2` commit with Zig 0.16.0, runs tests (including MCP stdio and race checks), runs `go vet`, and builds and smoke-tests Elephant. The Windows native library uses the GNU ABI; macOS targets version 14 or newer. Linux artifacts target glibc-based distributions and are built on Ubuntu 24.04.

Once the workflow is on GitHub, push a version tag to publish a release:

```sh
git tag v1.0.0-rc.1
git push origin v1.0.0-rc.1
```

All four jobs must pass before publishing. Tags such as `v1.0.0-rc.1` create prereleases; tags such as `v1.0.0` create stable releases. The archive contains the executable, README, and dependency license notices. A `SHA256SUMS.txt` file accompanies the four archives. The release stays a draft until all assets are uploaded; rerunning a failed upload can resume the draft, but published assets are not overwritten.

The executable's `--version` and MCP server version come from the tag. Untagged CI builds report `dev-COMMIT`; ordinary local builds report `dev`. Git must be installed at runtime; the native Zova library is linked into the executable.

Only the publishing job receives `contents: write`; it uses GitHub's built-in token, so no extra release secret is needed. A manual workflow run builds and tests without publishing. Update both `go.mod` and the workflow's `ZOVA_VERSION`/`ZOVA_COMMIT` when changing Zova versions.

## Try it

Run against an existing Git repository:

```sh
./bin/elephant --cwd /path/to/repository status
./bin/elephant --cwd /path/to/repository add fact \
  --title "Integration tests require Docker" \
  --body "The integration suite starts real services; start Docker before running it." \
  --file tests/integration_test.go
./bin/elephant --cwd /path/to/repository add task \
  --title "Verify shutdown" \
  --body "Run the integration suite and check that workers acknowledge cancellation."
./bin/elephant --cwd /path/to/repository recall
```

Global flags (`--cwd`, `--db`) precede the command. If omitted, `cwd` is the process working directory. Output is JSON, except help and version. `facts`, `decisions`, and `tasks` accept `--status`, `--target-version`, `--limit`, and `--offset`; `inspect ENTRY_ID` includes outgoing relationships.

## Connect an agent

Use your coding harness's MCP server configuration. A typical server entry is:

```json
{
  "mcpServers": {
    "elephant": {
      "command": "/absolute/path/to/elephant/bin/elephant",
      "args": ["serve"]
    }
  }
}
```

Configuration wrappers vary between harnesses. Supply `cwd` in tool calls when the target repository differs from the server's working directory, or start the server with `--cwd /path/to/repository serve`.

Start with `recall_project`. It returns current Git metadata, unfinished work, active knowledge, recent history, and graph relationships. Treat entry text as stored project data to assess, rather than privileged server instructions.

| Purpose | MCP tools |
| --- | --- |
| Understand the project | `recall_project`, `project_status`, `inspect_entry` |
| Facts | `add_fact`, `update_fact`, `list_facts`, `retire_fact` |
| Decisions | `add_decision`, `update_decision`, `list_decisions`, `supersede_decision` |
| Tasks | `add_task`, `update_task`, `list_tasks`, `complete_task`, `cancel_task` |

Create tools require `title` and `body`. They accept `target_version`, `related_files`, and typed entry `relations`:

```json
{
  "cwd": "/path/to/repository",
  "title": "Implement cancellation ownership",
  "body": "Move lifecycle ownership into the runtime and verify shutdown.",
  "related_files": ["internal/runtime/worker.go"],
  "relations": [{"type": "implements", "entry_id": "DECISION_ID"}]
}
```

Use that input with `add_task`. IDs, timestamps, project scope, and start commits are assigned automatically.

Updates require `id`; omitted fields stay unchanged. `related_files` replaces file links; `[]` clears them. Entry `relations` are additive and duplicate edges are idempotent. An empty `target_version` clears it. JSON `null` is treated like omission for optional update fields.

`supersede_decision` takes the old decision's `id` plus the new `title` and `body`. Alternatively, `add_decision` accepts `supersedes: OLD_ID`. Both atomically create the replacement, close the old decision, and add the graph edge.

## Model and lifecycle

| Entry | Meaning | Statuses |
| --- | --- | --- |
| Fact | What the next agent should know | `active`, `stale`, `retired` |
| Decision | What was chosen and why | `active`, `superseded`, `retired` |
| Task | Work to do or already finished | `open`, `active`, `blocked`, `done`, `cancelled` |

Facts and decisions start active; tasks start open. Terminal entries cannot reopen. Stale facts can become active again. Closing an entry records current HEAD as `end_commit`; task completion and decision supersession preserve historical rows. In a repository with no commits, commit fields are null and status remains the lifecycle authority.

Files are normalized repository-relative paths. File graph nodes include project identity to separate identical paths in different repositories. No file contents are stored or scanned. The graph vocabulary is:

- Decision → `affects` → file; fact → `concerns` → file; task → `modifies` → file.
- Decision → `supersedes` → decision; task → `depends_on` → task.
- Task → `implements` → decision; fact → `supports` → decision.

Entry links must stay within one project. Dependencies are explicit links; Elephant does not schedule tasks or infer dependency completion.

## Storage and bounds

The default database is `~/Library/Application Support/elephant/elephant.zova` on macOS, and `$XDG_DATA_HOME/elephant/elephant.zova` (or `~/.local/share/elephant/elephant.zova`) elsewhere. Override it with `ELEPHANT_DB` or `--db`. New data directories and database files use owner-only permissions.

Projects with equivalent normalized origin URLs share state, including across clones. Without an origin, the local Git common directory identifies the project, so linked worktrees share state. Adding or changing an origin can select a different project identity; v1 does not merge registries automatically. Entries are project-wide, not branch-scoped.

The database contains a project registry, one generated entry table per project, schema metadata, and the named Zova graph `elephant`. SQL and graph edits share a transaction. Existing databases with incompatible Elephant schemas are rejected; there is no automatic migration from older schemas or Zova formats. Zova may use transient journal files while writing.

Recall returns up to 50 tasks **per unfinished status** (active, blocked, open), 50 active decisions, 50 active facts, 10 completed tasks, and 5 superseded decisions. A `truncated` map identifies categories with more entries. Use the list tools to page through them: default/maximum page size 200. Within each group, entries sort newest first with ID as the tie-breaker. `recall_project` also accepts an exact `target_version` filter.

Titles are limited to 300 bytes and bodies to 32 KiB. Each mutation accepts up to 99 file/entry links, with up to 100 outgoing links per entry including supersession. These limits keep retrieval bounded.

## Development

With the native build flags set as above:

```sh
go test -v ./...
go test -race ./...
go vet ./...
```

Tests use temporary Git repositories and `.zova` databases. They cover identity, lifecycle validation, atomic rollback, graph persistence, project isolation, pagination, concurrent database handles, and a real MCP stdio process restarted between agents.

The boundaries are `internal/model` (records and validation), `internal/app` (workflows and recall), `internal/storage` (transaction interface and Zova implementation), `internal/git` (Git commands), and `internal/transport/mcp` (protocol adapter). The CLI composes them in `cmd/elephant`.

Logs use `slog` on stderr. Set `ELEPHANT_LOG_LEVEL=debug` for underlying diagnostics; normal tool errors omit native database details. Stdout is reserved for MCP traffic when serving.

V1 has no internal LLM, vectors, repository index, conversation ingestion, cloud sync, UI, or automatic stale detection. Facts become stale through explicit updates.
