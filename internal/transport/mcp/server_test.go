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

// assertToolSet requires every named tool to be advertised exactly once. It
// replaces brittle exact-count assertions so stacked features compose without
// rewriting this test on every branch.
func assertToolSet(t *testing.T, tools *sdk.ListToolsResult, required []string) {
	t.Helper()
	seen := map[string]int{}
	for _, tool := range tools.Tools {
		seen[tool.Name]++
	}
	for name, count := range seen {
		if count != 1 {
			t.Fatalf("tool %q advertised %d times", name, count)
		}
	}
	for _, name := range required {
		if seen[name] != 1 {
			t.Fatalf("required tool %q missing (have %v)", name, tools.Tools)
		}
	}
}

func TestMCPToolsValidationAndContinuity(t *testing.T) {
	// The budget covers process startup, database open, and several
	// sequential tool calls; Windows CI runners are significantly slower,
	// so allow a full minute instead of failing on general slowness.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	assertToolSet(t, tools, []string{
		"recall_project", "project_status", "inspect_entry",
		"add_fact", "update_fact", "list_facts", "retire_fact",
		"add_decision", "update_decision", "list_decisions", "supersede_decision",
		"add_task", "update_task", "list_tasks", "complete_task", "cancel_task",
		"list_remotes", "ensure_remote_project",
		"remote_send_fact", "remote_send_decision", "remote_send_task", "remote_recall",
		"remote_inbox", "remote_diff", "adopt_entry",
		"add_evidence", "list_evidence", "verify_evidence", "refresh_evidence", "remove_evidence",
		"add_checkpoint", "list_checkpoints",
	})
	rr, re := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_remotes", Arguments: map[string]any{}})
	if re != nil || rr.IsError {
		t.Fatal(rr, re)
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
