package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/storage/zova"
)

func TestMCPToolsValidationAndContinuity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cwd := t.TempDir()
	cmd := exec.Command("git", "init", "-q", cwd)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	db, err := zova.Open(filepath.Join(t.TempDir(), "mcp.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := New(app.New(db), cwd)
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 16 {
		t.Fatalf("tools=%d", len(tools.Tools))
	}
	for _, args := range []map[string]any{{"title": "missing body"}, {"title": "title", "body": " "}, {"title": "title", "body": "body", "related_files": []string{"../escape"}}} {
		r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "add_fact", Arguments: args})
		if err == nil && !r.IsError {
			t.Fatal("accepted invalid input", args)
		}
	}
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "add_task", Arguments: map[string]any{"title": "Task", "body": "Useful work"}})
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	data, _ := json.Marshal(r.StructuredContent)
	var record app.Record
	if err = json.Unmarshal(data, &record); err != nil || record.Entry.ID == "" {
		t.Fatalf("%s %v", data, err)
	}
	r, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "complete_task", Arguments: map[string]any{"id": record.Entry.ID}})
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	r, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "recall_project", Arguments: map[string]any{}})
	if err != nil || r.IsError {
		t.Fatal(r, err)
	}
	data, _ = json.Marshal(r.StructuredContent)
	var packet app.RecallResult
	if err = json.Unmarshal(data, &packet); err != nil || len(packet.RecentCompleted) != 1 {
		t.Fatalf("%s %v", data, err)
	}
}
