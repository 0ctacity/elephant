package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	transport "elephant/internal/transport/mcp"
)

func TestVersionAndCLI(t *testing.T) {
	previous := transport.Version
	transport.Version = "1.2.3-rc.4"
	t.Cleanup(func() { transport.Version = previous })
	var out, logs bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out, &logs); err != nil || out.String() != "elephant 1.2.3-rc.4\n" {
		t.Fatal(out.String(), err)
	}
	cwd := t.TempDir()
	cmd := exec.Command("git", "init", "-q", cwd)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	t.Setenv("ELEPHANT_DB", filepath.Join(t.TempDir(), "cli.zova"))
	out.Reset()
	if err := run(context.Background(), []string{"--cwd", cwd, "add", "fact", "--title", "Testing", "--body", "Use real repositories"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), []string{"--cwd", cwd, "recall"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "Testing") {
		t.Fatal(out.String())
	}
}
func TestExportImportBackupCLI(t *testing.T) {
	cwd := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", cwd},
		{"-C", cwd, "remote", "add", "origin", "https://github.com/test/cli-portable.git"},
		{"-C", cwd, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"},
	} {
		if b, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	t.Setenv("ELEPHANT_DB", filepath.Join(t.TempDir(), "cli-portable.zova"))
	ctx := context.Background()
	var out, logs bytes.Buffer
	if err := run(ctx, []string{"--cwd", cwd, "add", "fact", "--title", "Portable", "--body", "Round trip"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	runExport := func() string {
		t.Helper()
		out.Reset()
		if err := run(ctx, []string{"--cwd", cwd, "export"}, &out, &logs); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first, second := runExport(), runExport()
	if first != second || !json.Valid([]byte(first)) {
		t.Fatal("export is not deterministic")
	}
	archive := filepath.Join(t.TempDir(), "archive.json")
	if err := os.WriteFile(archive, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(ctx, []string{"--cwd", cwd, "import", "--as-remote", "cli", archive}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(ctx, []string{"--cwd", cwd, "import", "--as-remote", "cli", archive}, &out, &logs); err != nil {
		t.Fatalf("reimport must be idempotent: %v", err)
	}
	backup := filepath.Join(t.TempDir(), "backup.zova")
	out.Reset()
	if err := run(ctx, []string{"backup", "--output", backup}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(ctx, []string{"--db", backup, "--cwd", cwd, "recall"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Portable") {
		t.Fatalf("restored database lost entries: %s", out.String())
	}
}

func TestStdioProcess(t *testing.T) {
	if os.Getenv("ELEPHANT_TEST_PROCESS") == "1" {
		if err := run(context.Background(), []string{"serve"}, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cwd := t.TempDir()
	cmd := exec.Command("git", "init", "-q", cwd)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	database := filepath.Join(t.TempDir(), "stdio.zova")
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioProcess$")
	command.Env = append(os.Environ(), "ELEPHANT_TEST_PROCESS=1", "ELEPHANT_DB="+database)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "stdio-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatalf("connect: %v %s", err, stderr.String())
	}
	for _, kind := range []string{"fact", "decision", "task"} {
		r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "add_" + kind, Arguments: map[string]any{"cwd": cwd, "title": "Remember " + kind, "body": "Context for the next agent", "related_files": []string{"main.go"}}})
		if err != nil || r.IsError {
			t.Fatal(r, err)
		}
	}
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "recall_project", Arguments: map[string]any{"cwd": cwd}})
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioProcess$")
	restarted.Env = command.Env
	restarted.Stderr = &stderr
	session, err = client.Connect(ctx, &sdk.CommandTransport{Command: restarted}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	r, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "recall_project", Arguments: map[string]any{"cwd": cwd}})
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	data, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"fact", "decision", "task"} {
		if !bytes.Contains(data, []byte("Remember "+kind)) {
			t.Fatalf("missing persisted %s: %s", kind, data)
		}
	}
}

func TestRemoteCLIRegistry(t *testing.T) {
	t.Setenv("ELEPHANT_DB", filepath.Join(t.TempDir(), "remote.zova"))
	var out, logs bytes.Buffer
	for _, args := range [][]string{{"identity"}, {"remote", "add", "fedora", "--host", "fedora"}, {"remote", "list"}, {"remote", "remove", "fedora"}} {
		out.Reset()
		if err := run(context.Background(), args, &out, &logs); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestReceiveProcessAndRemoteRecall(t *testing.T) {
	if os.Getenv("ELEPHANT_RECEIVE_TEST") == "1" {
		if err := run(context.Background(), []string{"receive"}, os.Stdout, os.Stderr); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database := filepath.Join(t.TempDir(), "receive.zova")
	cwd := t.TempDir()
	for _, args := range [][]string{{"init", "-q", cwd}, {"-C", cwd, "remote", "add", "origin", "https://github.com/test/cli.git"}} {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	id := func() string { return uuid.Must(uuid.NewV7()).String() }
	now := time.Now().UTC()
	sender := id()
	m := app.Message{ProtocolVersion: 1, MessageID: id(), SenderElephantID: sender, Project: app.ProjectRef{Identity: "github.com/test/cli", Name: "cli"}, Operation: "entry.send", Entry: &model.Entry{ID: id(), Kind: model.Decision, Title: "Remote choice", Body: "Rationale", ActorID: "claude-session", Status: "active", CreatedAt: now, UpdatedAt: now}}
	data, _ := json.Marshal(m)
	for i := 0; i < 2; i++ {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReceiveProcessAndRemoteRecall$")
		command.Env = append(os.Environ(), "ELEPHANT_RECEIVE_TEST=1", "ELEPHANT_DB="+database)
		command.Stdin = bytes.NewReader(data)
		var logs bytes.Buffer
		command.Stderr = &logs
		out, err := command.Output()
		if err != nil {
			t.Fatal(err, logs.String())
		}
		var response app.Response
		if err = json.Unmarshal(out, &response); err != nil || response.Error != "" || response.EntryID != m.Entry.ID {
			t.Fatal(string(out), err)
		}
	}
	var out, logs bytes.Buffer
	if err := run(ctx, []string{"--db", database, "--cwd", cwd, "recall", "--remote", sender}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var received app.RecallResult
	if err := json.Unmarshal(out.Bytes(), &received); err != nil || len(received.Decisions) != 1 || received.Decisions[0].ActorID != "claude-session" {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := run(ctx, []string{"--db", database, "--cwd", cwd, "recall"}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	var local app.RecallResult
	if err := json.Unmarshal(out.Bytes(), &local); err != nil || len(local.Decisions) != 0 {
		t.Fatal(out.String(), err)
	}
}

func TestSearchCLIFilters(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", cwd).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	database := filepath.Join(t.TempDir(), "searchcli.zova")
	svc := func() *app.Service {
		db, err := zova.Open(database)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return app.New(db)
	}()
	if _, err := svc.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Alpha gateway", Body: "cli filters"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Beta gateway", Body: "cli filters"}); err != nil {
		t.Fatal(err)
	}
	var out, logs bytes.Buffer
	runOK := func(args []string) string {
		out.Reset()
		if err := run(ctx, args, &out, &logs); err != nil {
			t.Fatalf("%v: %v %s", args, err, logs.String())
		}
		return out.String()
	}
	q := func(extra ...string) []string {
		args := append([]string{"--db", database, "--cwd", cwd, "search", "--limit", "50"}, extra...)
		return append(args, "gateway")
	}
	var all []struct {
		Entry struct {
			ID        string `json:"id"`
			UpdatedAt string `json:"updated_at"`
		} `json:"entry"`
		Match []string `json:"match"`
	}
	if err := json.Unmarshal([]byte(runOK(q())), &all); err != nil || len(all) != 2 {
		t.Fatalf("search: %d %v %s", len(all), err, out.String())
	}
	// Time filters compose with the text query.
	betaStamp := strings.Replace(all[0].Entry.UpdatedAt, "+0000", "Z", 1)
	alphaStamp := strings.Replace(all[1].Entry.UpdatedAt, "+0000", "Z", 1)
	var newer []map[string]any
	if err := json.Unmarshal([]byte(runOK(q("--updated-after", betaStamp))), &newer); err != nil || len(newer) != 1 {
		t.Fatalf("updated-after: %d %v %s", len(newer), err, out.String())
	}
	var older []map[string]any
	if err := json.Unmarshal([]byte(runOK(q("--updated-before", alphaStamp))), &older); err != nil || len(older) != 1 {
		t.Fatalf("updated-before: %d %v %s", len(older), err, out.String())
	}
	// Kind and status filters still compose.
	var onlyFacts []map[string]any
	if err := json.Unmarshal([]byte(runOK(q("--kind", "fact"))), &onlyFacts); err != nil || len(onlyFacts) != 1 {
		t.Fatalf("kind: %d %v %s", len(onlyFacts), err, out.String())
	}
	// Invalid RFC3339 is rejected as invalid input, not a server error.
	out.Reset()
	if err := run(ctx, q("--updated-after", "yesterday"), &out, &logs); err == nil {
		t.Fatal("invalid time accepted")
	}
}
