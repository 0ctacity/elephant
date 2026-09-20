package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func TestFileURIToPath(t *testing.T) {
	for _, tc := range []struct {
		uri  string
		want string
	}{
		{"file:///repo/share", "/repo/share"},
		{"file:///tmp/with%20space/x.go", "/tmp/with space/x.go"},
		{"file:///C:/repo/share", "C:/repo/share"},
		{"file:///c:/repo/share", "c:/repo/share"},
		{"file://localhost/C:/repo", "C:/repo"},
		{"file://build-host/share/dir", "//build-host/share/dir"},
		{"file:C:/repo", "C:/repo"},
		{"file:///C|/repo", "C:/repo"},
		{"file:///", "/"},
	} {
		got, err := FileURIToPath(tc.uri)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q,%v want %q", tc.uri, got, err, tc.want)
		}
	}
	for _, uri := range []string{"", "https://example.com/x", "untitled:Untitled-1", "file://", "file://?q=1", "file:///%zz", "relative/path"} {
		if got, err := FileURIToPath(uri); err == nil {
			t.Errorf("%q: accepted as %q", uri, got)
		}
	}
}

func TestResolveCWDSelection(t *testing.T) {
	ctx := context.Background()
	lister := func(roots ...*sdk.Root) RootsLister {
		return func(context.Context) (*sdk.ListRootsResult, error) {
			return &sdk.ListRootsResult{Roots: roots}, nil
		}
	}
	root := func(uri string) *sdk.Root { return &sdk.Root{URI: uri} }
	if got := ResolveCWD(ctx, "/explicit", lister(root("file:///other")), "/default"); got != "/explicit" {
		t.Fatalf("explicit lost: %q", got)
	}
	if got := ResolveCWD(ctx, "", nil, "/default"); got != "/default" {
		t.Fatalf("nil lister: %q", got)
	}
	errLister := RootsLister(func(context.Context) (*sdk.ListRootsResult, error) { return nil, context.DeadlineExceeded })
	if got := ResolveCWD(ctx, "", errLister, "/default"); got != "/default" {
		t.Fatalf("error lister: %q", got)
	}
	if got := ResolveCWD(ctx, "", lister(), "/default"); got != "/default" {
		t.Fatalf("no roots: %q", got)
	}
	// First usable file root wins; invalid and non-file roots are skipped.
	got := ResolveCWD(ctx, "", lister(root("::bad"), root("https://example.com/x"), root("file:///second"), root("file:///third")), "/default")
	if got != "/second" {
		t.Fatalf("multiple roots: %q", got)
	}
	if _, ok := FirstFileRoot([]*sdk.Root{nil}); ok {
		t.Fatal("nil root accepted")
	}
}

func TestRootsProtocolAdvertisementAndChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// A probe tool that resolves scope through the SEP-2322 multi-round-trip
	// flow: the first call requests client roots, the retry consumes them.
	// This is the only server-initiated roots path the negotiated protocol
	// allows.
	probe := sdk.NewServer(&sdk.Implementation{Name: "probe", Version: "1"}, nil)
	sdk.AddTool(probe, &sdk.Tool{Name: "where"}, func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, string, error) {
		if len(req.Params.InputResponses) == 0 {
			return &sdk.CallToolResult{InputRequests: sdk.InputRequestMap{"client_roots": &sdk.ListRootsParams{}}}, "", nil
		}
		roots, ok := req.Params.InputResponses["client_roots"].(*sdk.ListRootsResult)
		if !ok {
			return nil, "", fmt.Errorf("missing roots response")
		}
		return nil, ResolveCWD(ctx, "", func(context.Context) (*sdk.ListRootsResult, error) { return roots, nil }, "/default"), nil
	})
	st, ct := sdk.NewInMemoryTransports()
	ss, err := probe.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "roots-test", Version: "1"}, &sdk.ClientOptions{
		Capabilities: &sdk.ClientCapabilities{RootsV2: &sdk.RootCapabilities{ListChanged: true}},
	})
	client.AddRoots(&sdk.Root{URI: "file:///first", Name: "first"}, &sdk.Root{URI: "https://example.com/remote", Name: "remote"})
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// The client actually advertised and responded to roots through the
	// protocol: the first file root resolves.
	call := func() string {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "where"})
		if err != nil || res.IsError {
			t.Fatalf("%v %+v", err, res)
		}
		var cwd string
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, &cwd); err != nil {
			t.Fatalf("%s %v", data, err)
		}
		return cwd
	}
	if got := call(); got != "/first" {
		t.Fatalf("advertised roots did not resolve: %q", got)
	}
	// Roots-changed notification: the updated set resolves on the next call.
	client.AddRoots(&sdk.Root{URI: "file:///added", Name: "added"})
	client.RemoveRoots("file:///first")
	if got := call(); got != "/added" {
		t.Fatalf("changed roots did not resolve: %q", got)
	}
	// A client advertising no usable roots falls back to the default.
	bareProbe := sdk.NewServer(&sdk.Implementation{Name: "bare-probe", Version: "1"}, nil)
	sdk.AddTool(bareProbe, &sdk.Tool{Name: "where"}, func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, string, error) {
		if len(req.Params.InputResponses) == 0 {
			return &sdk.CallToolResult{InputRequests: sdk.InputRequestMap{"client_roots": &sdk.ListRootsParams{}}}, "", nil
		}
		roots, _ := req.Params.InputResponses["client_roots"].(*sdk.ListRootsResult)
		var list []*sdk.Root
		if roots != nil {
			list = roots.Roots
		}
		if path, ok := FirstFileRoot(list); ok {
			return nil, path, nil
		}
		return nil, "/default", nil
	})
	st2, ct2 := sdk.NewInMemoryTransports()
	ss2, err := bareProbe.Connect(ctx, st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss2.Close()
	bare := sdk.NewClient(&sdk.Implementation{Name: "bare", Version: "1"}, nil)
	session2, err := bare.Connect(ctx, ct2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session2.Close()
	res, err := session2.CallTool(ctx, &sdk.CallToolParams{Name: "where"})
	if err != nil || res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestAdvertisedCapabilitiesMatchHandlers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cwd := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", cwd).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	db, err := zova.Open(filepath.Join(t.TempDir(), "caps.zova"))
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
	client := sdk.NewClient(&sdk.Implementation{Name: "caps", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	res, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range res.Resources {
		seen[r.URI] = true
	}
	for _, uri := range []string{resourceRecall, resourceTasks, resourceDecisions} {
		if !seen[uri] {
			t.Fatalf("resource %q not advertised: %v", uri, seen)
		}
	}
	if len(res.Resources) != 3 {
		t.Fatalf("unexpected resources: %v", seen)
	}
	pr, err := session.ListPrompts(ctx, nil)
	if err != nil || len(pr.Prompts) != 1 {
		t.Fatalf("%+v %v", pr, err)
	}
	prompt := pr.Prompts[0]
	if prompt.Name != "resume_project" {
		t.Fatalf("prompt=%v", prompt.Name)
	}
	args := map[string]bool{}
	for _, a := range prompt.Arguments {
		args[a.Name] = true
	}
	if !args["cwd"] || !args["target_version"] {
		t.Fatalf("prompt args=%v", args)
	}
}

func TestTasksResourceIsGloballyNewestFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cwd := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", cwd).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	db, err := zova.Open(filepath.Join(t.TempDir(), "newest.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := app.New(db)
	mk := func(title string) string {
		rec, err := svc.Add(ctx, cwd, model.Task, app.CreateInput{Title: title, Body: "work"})
		if err != nil {
			t.Fatal(err)
		}
		return rec.Entry.ID
	}
	oldOpen := mk("old open")
	mid := mk("middle")
	// Reopen recency: move the oldest entry to active so statuses interleave.
	active := "active"
	if _, err = svc.Update(ctx, cwd, model.Task, oldOpen, app.UpdateInput{Status: &active}); err != nil {
		t.Fatal(err)
	}
	newest := mk("newest open")
	server := New(svc, cwd)
	st, ct := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "order", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	rr, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: resourceTasks})
	if err != nil {
		t.Fatal(err)
	}
	var entries []model.Entry
	if err = json.Unmarshal([]byte(rr.Contents[0].Text), &entries); err != nil {
		t.Fatal(err)
	}
	want := []string{newest, oldOpen, mid}
	if len(entries) != len(want) {
		t.Fatalf("entries=%v", entries)
	}
	for i, id := range want {
		if entries[i].ID != id {
			t.Fatalf("position %d: got %s want %s (%v)", i, entries[i].ID, id, entries)
		}
	}
}
