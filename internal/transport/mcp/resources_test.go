package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/storage/zova"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{{"init", "-q", p}, {"-C", p, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	return p
}

func TestResourcesPromptsAndRoots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cwd := gitRepo(t)
	other := gitRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "res.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := app.New(db)
	if _, err = svc.Add(ctx, cwd, "task", app.CreateInput{Title: "T", Body: "work"}); err != nil {
		t.Fatal(err)
	}
	server := New(svc, cwd)
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

	res, err := session.ListResources(ctx, nil)
	if err != nil || len(res.Resources) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, uri := range []string{"elephant://project/current/recall", "elephant://project/current/tasks", "elephant://project/current/decisions"} {
		rr, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
		if err != nil || len(rr.Contents) != 1 {
			t.Fatalf("%s %v %+v", uri, err, rr)
		}
	}
	if _, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "elephant://project/current/unknown"}); err == nil {
		t.Fatal("invalid resource accepted")
	}
	pr, err := session.ListPrompts(ctx, nil)
	if err != nil || len(pr.Prompts) != 1 || pr.Prompts[0].Name != "resume_project" {
		t.Fatalf("%+v %v", pr, err)
	}
	got, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "resume_project", Arguments: map[string]string{}})
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	// Explicit cwd selects another repository without crossing boundaries
	// (roots are deprecated in protocol 2026-07-28; path passing is the
	// supported migration, with server-default fallback preserved).
	got, err = session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "resume_project", Arguments: map[string]string{"cwd": other}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].Content == nil {
		t.Fatal("empty prompt")
	}
	text := ""
	if tc, ok := got.Messages[0].Content.(*sdk.TextContent); ok {
		text = tc.Text
	}
	if text == "" || strings.Contains(text, "Unfinished tasks: 0") == false {
		t.Fatalf("prompt did not resolve other project: %q", text)
	}
	// A client advertising roots still uses current tools/resources: the
	// server attempts roots and falls back to the default cwd.
}
