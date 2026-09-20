package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"elephant/internal/app"
	"elephant/internal/model"
)

func runCheckpoint(ctx context.Context, s *app.Service, cwd string, args []string, logs io.Writer) (any, error) {
	if len(args) > 0 && args[0] == "checkpoints" {
		args = args[1:]
	}
	f := flag.NewFlagSet("checkpoint", flag.ContinueOnError)
	f.SetOutput(logs)
	summary := f.String("summary", "", "session summary (required)")
	completed := f.String("completed", "", "completed work")
	next := f.String("next", "", "next actions")
	commands := f.String("commands", "", "commands or checks run")
	failures := f.String("failures", "", "unresolved failures")
	start := f.String("start-commit", "", "starting commit (defaults to previous checkpoint end)")
	var files, relations fileFlags
	f.Var(&files, "file", "related repository-relative file; repeatable")
	f.Var(&relations, "relation", "TYPE:ENTRY_UUID; repeatable")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, model.ErrInvalidInput
	}
	in := app.CheckpointInput{Summary: *summary, Completed: *completed, Next: *next, Commands: *commands, Failures: *failures, StartCommit: *start, RelatedFiles: files}
	for _, raw := range relations {
		typ, id, ok := strings.Cut(raw, ":")
		if !ok || typ == "" || id == "" {
			return nil, fmt.Errorf("%w: relation must be TYPE:ENTRY_UUID", model.ErrInvalidInput)
		}
		in.Relations = append(in.Relations, app.RelationInput{Type: typ, EntryID: id})
	}
	return s.AddCheckpoint(ctx, cwd, in)
}

func runCheckpoints(ctx context.Context, s *app.Service, cwd string, args []string, logs io.Writer) (any, error) {
	f := flag.NewFlagSet("checkpoints", flag.ContinueOnError)
	f.SetOutput(logs)
	limit := f.Int("limit", 10, "maximum checkpoints (1-200)")
	offset := f.Int("offset", 0, "pagination offset")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, model.ErrInvalidInput
	}
	return s.ListCheckpoints(ctx, cwd, *limit, *offset)
}
