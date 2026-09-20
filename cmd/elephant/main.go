package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
	transport "elephant/internal/transport/mcp"
	"elephant/internal/transport/receive"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "elephant:", transport.PublicError(err))
		os.Exit(1)
	}
}

const usage = `Elephant — local continuity for coding agents

Usage: elephant [--cwd DIR] [--db FILE.zova] COMMAND

  serve                 Run the MCP stdio server (also the default command)
  setup AGENT           Register the MCP server with codex or opencode
  doctor [--json]       Diagnose executable, database, Git, MCP, actor, remotes
  recall                Show saved project context and unfinished work
  identity              Show this installation’s stable Elephant ID
  remote                Manage peers, ensure projects, send entries, or fetch state
  receive               Process one machine JSON message from stdin
  status                Show project identity and Git metadata
  facts|decisions|tasks  List entries (--status, --target-version, --limit, --offset)
  inspect ID            Show an entry and its relationships
  adopt ENTRY_ID --from NAME_OR_UUID  Promote a received entry into local truth
  evidence add ENTRY_ID --file PATH [--line N]
  evidence list|verify ENTRY_ID
  evidence refresh ENTRY_ID [--evidence EVIDENCE_ID]
  evidence remove EVIDENCE_ID
  checkpoint --summary TEXT [--completed TEXT ...] [--next TEXT ...]
             [--commands TEXT ...] [--failures TEXT ...]
             [--file PATH ...] [--relation TYPE:ID ...] [--start-commit COMMIT]
  checkpoints [--limit N] [--offset N]  List session checkpoints
  export [--project]    Write versioned project JSON to stdout
  import FILE [--as-remote NAME]  Load an export into a separate source table
  backup --output FILE.zova  Write a consistent database snapshot
  add fact|decision|task --title TEXT --body TEXT [--target-version TEXT] [--file PATH ...]
  remote send fact --title TEXT --body TEXT [--evidence EVIDENCE_ID ...]
                   [--file PATH ...] [--relation TYPE:ENTRY_UUID ...]
  --version             Print version

Use recall --remote NAME_OR_UUID for a locally stored remote source table.
remote inbox groups received additions by source; remote diff NAME compares
one source table with local state; adopt creates a new local entry with
provenance and is idempotent.
setup accepts --actor ID and --config FILE; unsupported agents print an example.
doctor prints text by default and structured JSON with --json.
recall includes a bounded evidence section for active facts; verification
reports unchanged, changed, missing, or unavailable and never retires a fact.
'remote send --evidence' carries a local evidence row with the sent fact;
the receiver re-verifies it against its own checkout on recall.
recall includes the latest checkpoint before older project context.
Export output is deterministic for unchanged state; import validates fully
before committing and never overwrites local truth.
ELEPHANT_ACTOR_ID identifies the actor creating rows (default: unknown).
ELEPHANT_DB overrides the default database path.
ELEPHANT_LOG_LEVEL accepts debug, info, warn, or error. Logs go to stderr.
`

func defaultDB() (string, error) {
	if p := os.Getenv("ELEPHANT_DB"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "elephant", "elephant.zova"), nil
	}
	root := os.Getenv("XDG_DATA_HOME")
	if root == "" {
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "elephant", "elephant.zova"), nil
}

type fileFlags []string

func (f *fileFlags) String() string     { return fmt.Sprint([]string(*f)) }
func (f *fileFlags) Set(s string) error { *f = append(*f, s); return nil }

func readImportFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("%w: import file too large", model.ErrInvalidInput)
	}
	return data, nil
}
func run(ctx context.Context, args []string, out, logs io.Writer) error {
	fs := flag.NewFlagSet("elephant", flag.ContinueOnError)
	fs.SetOutput(logs)
	cwd := fs.String("cwd", "", "repository working directory")
	dbPath := fs.String("db", "", "Zova database path")
	version := fs.Bool("version", false, "print version")
	fs.Usage = func() { fmt.Fprint(out, usage) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %v", model.ErrInvalidInput, err)
	}
	if *version {
		fmt.Fprintln(out, "elephant", transport.Version)
		return nil
	}
	var level slog.Level
	if raw := os.Getenv("ELEPHANT_LOG_LEVEL"); raw != "" {
		if err := level.UnmarshalText([]byte(raw)); err != nil {
			return fmt.Errorf("%w: ELEPHANT_LOG_LEVEL must be debug, info, warn, or error", model.ErrInvalidInput)
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: level})))
	if *cwd == "" {
		var err error
		*cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if *dbPath == "" {
		var err error
		*dbPath, err = defaultDB()
		if err != nil {
			return err
		}
	}
	command := "serve"
	rest := fs.Args()
	if len(rest) > 0 {
		command = rest[0]
		rest = rest[1:]
	}
	switch command {
	case "setup":
		return runSetup(ctx, out, logs, rest, *dbPath)
	case "doctor":
		return runDoctor(ctx, out, logs, rest, *dbPath)
	}
	db, err := zova.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	service := app.New(db)
	var result any
	switch command {
	case "receive":
		if len(rest) != 0 {
			return model.ErrInvalidInput
		}
		return receive.Handle(ctx, service, os.Stdin, out)
	case "identity":
		if len(rest) != 0 {
			return model.ErrInvalidInput
		}
		result, err = service.Identity(ctx)
	case "remote":
		result, err = runRemote(ctx, service, *cwd, rest, logs)
	case "serve":
		if len(rest) != 0 {
			return fmt.Errorf("%w: serve takes no arguments", model.ErrInvalidInput)
		}
		return transport.New(service, *cwd).Run(ctx, &sdk.StdioTransport{})
	case "recall":
		f := flag.NewFlagSet("recall", flag.ContinueOnError)
		f.SetOutput(logs)
		source := f.String("remote", "", "source Elephant UUID or registered name")
		if err = f.Parse(rest); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return model.ErrInvalidInput
		}
		if *source == "" {
			result, err = service.Recall(ctx, *cwd, "")
		} else {
			result, err = service.RecallRemote(ctx, *cwd, *source)
		}
	case "evidence":
		result, err = runEvidence(ctx, service, *cwd, rest, logs)
	case "status":
		if len(rest) != 0 {
			return model.ErrInvalidInput
		}
		result, err = service.Status(ctx, *cwd)
	case "facts", "decisions", "tasks":
		list := flag.NewFlagSet(command, flag.ContinueOnError)
		list.SetOutput(logs)
		status := list.String("status", "", "status filter")
		target := list.String("target-version", "", "version filter")
		limit := list.Int("limit", 200, "maximum entries (1–200)")
		offset := list.Int("offset", 0, "pagination offset")
		if err = list.Parse(rest); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalidInput, err)
		}
		if list.NArg() != 0 {
			return model.ErrInvalidInput
		}
		kinds := map[string]model.Kind{"facts": model.Fact, "decisions": model.Decision, "tasks": model.Task}
		result, err = service.List(ctx, *cwd, model.Filter{Kind: kinds[command], Status: *status, TargetVersion: *target, Limit: *limit, Offset: *offset})
	case "inspect":
		if len(rest) != 1 {
			return fmt.Errorf("%w: inspect requires one entry ID", model.ErrInvalidInput)
		}
		result, err = service.Get(ctx, *cwd, rest[0])
	case "checkpoint":
		result, err = runCheckpoint(ctx, service, *cwd, rest, logs)
	case "checkpoints":
		result, err = runCheckpoints(ctx, service, *cwd, rest, logs)
	case "add":
		if len(rest) == 0 {
			return fmt.Errorf("%w: add requires fact, decision, or task", model.ErrInvalidInput)
		}
		kind := model.Kind(rest[0])
		add := flag.NewFlagSet("add", flag.ContinueOnError)
		add.SetOutput(logs)
		title := add.String("title", "", "concise title")
		body := add.String("body", "", "context and reasoning")
		target := add.String("target-version", "", "release or milestone")
		var files fileFlags
		add.Var(&files, "file", "related repository-relative file; repeatable")
		if err = add.Parse(rest[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalidInput, err)
		}
		if add.NArg() != 0 {
			return model.ErrInvalidInput
		}
		in := app.CreateInput{Title: *title, Body: *body, RelatedFiles: files}
		if *target != "" {
			in.TargetVersion = target
		}
		result, err = service.Add(ctx, *cwd, kind, in)
	case "adopt":
		adopt := flag.NewFlagSet("adopt", flag.ContinueOnError)
		adopt.SetOutput(logs)
		from := adopt.String("from", "", "remote name or source Elephant UUID")
		if err = adopt.Parse(rest); err != nil {
			return err
		}
		if adopt.NArg() != 1 || *from == "" {
			return fmt.Errorf("%w: adopt requires ENTRY_ID --from NAME_OR_UUID", model.ErrInvalidInput)
		}
		result, err = service.Adopt(ctx, *cwd, adopt.Arg(0), *from)
	case "export":
		f := flag.NewFlagSet("export", flag.ContinueOnError)
		f.SetOutput(logs)
		project := f.String("project", "", "reserved scope selector (current project)")
		if err = f.Parse(rest); err != nil {
			return err
		}
		if f.NArg() != 0 || (*project != "" && *project != "current") {
			return fmt.Errorf("%w: export takes no positional arguments", model.ErrInvalidInput)
		}
		result, err = service.Export(ctx, *cwd)
	case "import":
		f := flag.NewFlagSet("import", flag.ContinueOnError)
		f.SetOutput(logs)
		asRemote := f.String("as-remote", "archive", "archive source name")
		if err = f.Parse(rest); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("%w: import requires one FILE argument", model.ErrInvalidInput)
		}
		data, readErr := readImportFile(f.Arg(0))
		if readErr != nil {
			return readErr
		}
		var env app.ExportEnvelope
		if err = json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("%w: invalid export JSON: %v", model.ErrInvalidInput, err)
		}
		result, err = service.Import(ctx, env, *asRemote)
	case "backup":
		f := flag.NewFlagSet("backup", flag.ContinueOnError)
		f.SetOutput(logs)
		output := f.String("output", "", "destination .zova file")
		if err = f.Parse(rest); err != nil {
			return err
		}
		if f.NArg() != 0 || *output == "" {
			return fmt.Errorf("%w: backup requires --output FILE.zova", model.ErrInvalidInput)
		}
		if filepath.Ext(*output) != ".zova" {
			return fmt.Errorf("%w: backup destination must end in .zova", model.ErrInvalidInput)
		}
		result, err = map[string]string{"backup": *output}, service.Backup(ctx, *output)
	default:
		return fmt.Errorf("%w: unknown command %q; use --help", model.ErrInvalidInput, command)
	}
	if err != nil {
		return err
	}
	return writeJSON(out, result)
}
