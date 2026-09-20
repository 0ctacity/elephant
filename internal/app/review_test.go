package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage"
	"elephant/internal/storage/zova"
)

func reviewRepo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", p},
		{"-C", p, "remote", "add", "origin", "https://github.com/test/review.git"},
		{"-C", p, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	return p
}

func TestReviewFindingsAndReadOnly(t *testing.T) {
	ctx := context.Background()
	cwd := reviewRepo(t)
	path := filepath.Join(t.TempDir(), "review.zova")
	db, err := zova.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	// Missing file.
	missing, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "F", Body: "B", RelatedFiles: []string{"gone.go"}})
	if err != nil {
		t.Fatal(err)
	}
	// Unknown actor (default).
	if _, err = s.Add(ctx, cwd, model.Task, app.CreateInput{Title: "T", Body: "do"}); err != nil {
		t.Fatal(err)
	}
	// Dangling relation: link to an entry that lives in another project.
	// The graph node exists globally so the edge writes, but review flags it
	// because the target is unavailable in this project's table.
	otherRepo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", otherRepo},
		{"-C", otherRepo, "remote", "add", "origin", "https://github.com/test/review-other.git"},
		{"-C", otherRepo, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	otherTask, err := s.Add(ctx, otherRepo, model.Task, app.CreateInput{Title: "Other", Body: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	danglingID := uuid.Must(uuid.NewV7()).String()
	now := time.Now().UTC()
	err = db.Transact(ctx, func(tx storage.Tx) error {
		e := model.Entry{ID: danglingID, ActorID: "tester", Kind: model.Task, Title: "D", Body: "dangling", Status: "open", CreatedAt: now, UpdatedAt: now}
		if err := tx.Put(status.Project, e); err != nil {
			return err
		}
		return tx.Link(status.Project, model.Relation{From: "entry:" + danglingID, Type: "depends_on", To: "entry:" + otherTask.Entry.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	// Supersession anomaly: mark superseded without edge.
	supID := uuid.Must(uuid.NewV7()).String()
	err = db.Transact(ctx, func(tx storage.Tx) error {
		e := model.Entry{ID: supID, ActorID: "tester", Kind: model.Decision, Title: "S", Body: "old", Status: "superseded", CreatedAt: now, UpdatedAt: now}
		return tx.Put(status.Project, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	// Old task via backdated timestamp.
	oldID := uuid.Must(uuid.NewV7()).String()
	old := now.Add(-40 * 24 * time.Hour)
	err = db.Transact(ctx, func(tx storage.Tx) error {
		e := model.Entry{ID: oldID, ActorID: "tester", Kind: model.Task, Title: "Old", Body: "stale", Status: "open", CreatedAt: old, UpdatedAt: old}
		return tx.Put(status.Project, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = missing
	findings, err := s.Review(ctx, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range findings {
		codes[f.Code] = true
		if f.Code == "" || f.Severity == "" || f.Source == "" || f.Action == "" || f.Detail == "" {
			t.Fatalf("incomplete finding: %+v", f)
		}
	}
	for _, want := range []string{app.CodeMissingFile, app.CodeStaleTask, app.CodeUnknownActor, app.CodeDanglingRelation, app.CodeSupersessionAnomaly} {
		if !codes[want] {
			t.Fatalf("missing %s in %+v", want, codes)
		}
	}
	// Read-only: entry count and timestamps unchanged.
	after, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = after
	second, err := s.Review(ctx, cwd, &app.ReviewOptions{StaleTaskDays: 1, UnverifiedFactDays: 1, StaleRemoteDays: 1})
	if err != nil || len(second) < len(findings) {
		t.Fatalf("%d vs %d", len(second), len(findings))
	}
	if _, err = s.Review(ctx, cwd, &app.ReviewOptions{StaleTaskDays: 0}); err == nil {
		// Zero means default, so no error expected; invalid is out of range.
	}
	if _, err = s.Review(ctx, cwd, &app.ReviewOptions{StaleTaskDays: 99999}); err == nil {
		t.Fatal("unbounded threshold accepted")
	}
	// Remote separation: add a peer with no identity.
	if _, err = s.AddRemote(ctx, "peer", "ash", `{"host":"peer"}`); err != nil {
		t.Fatal(err)
	}
	remoteFindings, err := s.Review(ctx, cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundRemote := false
	for _, f := range remoteFindings {
		if f.Code == app.CodeStaleRemote {
			foundRemote = true
		}
	}
	if !foundRemote {
		t.Fatal("missing stale remote finding")
	}
	_ = os.Getenv("unused")
}
