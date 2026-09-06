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
