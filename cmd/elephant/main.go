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
  recall                Show saved project context and unfinished work
  status                Show project identity and Git metadata
  facts|decisions|tasks  List entries (--status, --target-version, --limit, --offset)
  inspect ID            Show an entry and its relationships
  add fact|decision|task --title TEXT --body TEXT [--target-version TEXT] [--file PATH ...]
  --version             Print version

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
	db, err := zova.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	service := app.New(db)
	var result any
	switch command {
	case "serve":
		if len(rest) != 0 {
			return fmt.Errorf("%w: serve takes no arguments", model.ErrInvalidInput)
		}
		return transport.New(service, *cwd).Run(ctx, &sdk.StdioTransport{})
	case "recall", "status":
		if len(rest) != 0 {
			return fmt.Errorf("%w: unexpected arguments", model.ErrInvalidInput)
		}
		if command == "recall" {
			result, err = service.Recall(ctx, *cwd, "")
		} else {
			result, err = service.Status(ctx, *cwd)
		}
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
	default:
		return fmt.Errorf("%w: unknown command %q; use --help", model.ErrInvalidInput, command)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
