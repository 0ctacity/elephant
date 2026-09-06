package app_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage"
	"elephant/internal/storage/zova"
)

func repo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-qm", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = p
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	return p
}
func ptr(s string) *string { return &s }
func TestContinuityAcrossRestartAndProjects(t *testing.T) {
	ctx := context.Background()
	cwd := repo(t)
	other := repo(t)
	path := filepath.Join(t.TempDir(), "elephant.zova")
	store, err := zova.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := app.New(store)
	fact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Docker", Body: "Integration tests require Docker", RelatedFiles: []string{"tests/live.go"}})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Old choice", Body: "Reason"})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "New choice", Body: "Better reason", Supersedes: decision.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Add(ctx, cwd, model.Task, app.CreateInput{Title: "Implement", Body: "Build the choice", Relations: []app.RelationInput{{Type: "implements", EntryID: replacement.Entry.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Update(ctx, cwd, model.Task, task.Entry.ID, app.UpdateInput{Status: ptr("done")})
	if err != nil {
		t.Fatal(err)
	}
	if done.Entry.EndCommit == nil || done.Entry.StartCommit == nil {
		t.Fatal(done)
	}
	if _, err = s.Update(ctx, cwd, model.Task, task.Entry.ID, app.UpdateInput{Status: ptr("active")}); !errors.Is(err, model.ErrInvalidTransition) {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, other, fact.Entry.ID); !errors.Is(err, model.ErrEntryNotFound) {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = zova.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s = app.New(store)
	packet, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Facts) != 1 || len(packet.Decisions) != 1 || len(packet.RecentCompleted) != 1 || len(packet.RecentSuperseded) != 1 || len(packet.Tasks) != 0 || len(packet.Relations) < 2 {
		t.Fatalf("%+v", packet)
	}
	if packet.Git.Head == "" || packet.Facts[0].ID != fact.Entry.ID {
		t.Fatal(packet)
	}
}

func TestRecallResultBoundsAndVersionFilter(t *testing.T) {
	ctx := context.Background()
	cwd := repo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "bounded.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	status, err := s.Status(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transact(ctx, func(tx storage.Tx) error {
		for i := range 56 {
			e := model.Entry{ID: fmt.Sprintf("fact-%02d", i), Kind: model.Fact, Title: "Fact", Body: "Useful context", Status: "active", TargetVersion: ptr("v1"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := tx.Put(status.Project, e); err != nil {
				return err
			}
		}
		return tx.Put(status.Project, model.Entry{ID: "old", Kind: model.Fact, Title: "Stale", Body: "Old context", Status: "stale", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
	})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := s.Recall(ctx, cwd, "v1")
	if err != nil || len(packet.Facts) != 50 || !packet.Truncated["facts"] {
		t.Fatal(packet, err)
	}
	none, err := s.Recall(ctx, cwd, "v2")
	if err != nil || len(none.Facts) != 0 {
		t.Fatal(none, err)
	}
	page, err := s.List(ctx, cwd, model.Filter{Kind: model.Fact, Status: "active", TargetVersion: "v1", Limit: 10, Offset: 50})
	if err != nil || len(page) != 6 {
		t.Fatal(page, err)
	}
}

func TestSupersessionAtRelationLimitRemainsEditable(t *testing.T) {
	ctx := context.Background()
	cwd := repo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "limits.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	old, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Old", Body: "Reason"})
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 99)
	for i := range files {
		files[i] = fmt.Sprintf("file-%d.go", i)
	}
	next, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "New", Body: "Reason", RelatedFiles: files, Supersedes: old.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Update(ctx, cwd, model.Decision, next.Entry.ID, app.UpdateInput{Title: ptr("Clarified")})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Relations) != 100 {
		t.Fatal(len(updated.Relations))
	}
}

func TestAtomicValidationAndFileReplacement(t *testing.T) {
	ctx := context.Background()
	cwd := repo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "test.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	old, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Old", Body: "Reason", RelatedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "New", Body: "Reason", Supersedes: old.Entry.ID, Relations: []app.RelationInput{{Type: "supports", EntryID: "missing"}}})
	if err == nil {
		t.Fatal("invalid mutation accepted")
	}
	got, err := s.Get(ctx, cwd, old.Entry.ID)
	if err != nil || got.Entry.Status != "active" {
		t.Fatal(got, err)
	}
	files := []string{"b.go"}
	got, err = s.Update(ctx, cwd, model.Decision, old.Entry.ID, app.UpdateInput{RelatedFiles: &files})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Relations) != 1 || got.Relations[0].To == "a.go" {
		t.Fatal(got)
	}
	empty := []string{}
	got, err = s.Update(ctx, cwd, model.Decision, old.Entry.ID, app.UpdateInput{RelatedFiles: &empty})
	if err != nil || len(got.Relations) != 0 {
		t.Fatal(got, err)
	}
	if _, err = s.Update(ctx, cwd, model.Task, old.Entry.ID, app.UpdateInput{Status: ptr("done")}); !errors.Is(err, model.ErrInvalidKind) {
		t.Fatal(err)
	}
	entries, err := s.List(ctx, cwd, model.Filter{Kind: model.Decision})
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
}
