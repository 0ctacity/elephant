package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func evidenceRepo(t *testing.T) string {
	t.Helper()
	cwd := repo(t)
	if err := os.WriteFile(filepath.Join(cwd, "runtime.go"), []byte("package runtime\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func evidenceService(t *testing.T, name string) *app.Service {
	t.Helper()
	db, err := zova.Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return app.New(db)
}

func attachFact(t *testing.T, s *app.Service, cwd, path string, line int) (string, app.EvidenceView) {
	t.Helper()
	fact, err := s.Add(context.Background(), cwd, model.Fact, app.CreateInput{Title: "Fact", Body: "Context"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.AddEvidence(context.Background(), cwd, fact.Entry.ID, path, line)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != model.EvidenceUnchanged || view.EntryID != fact.Entry.ID || view.Path != path || view.Line != line {
		t.Fatalf("%+v", view)
	}
	if view.Commit == "" || view.Blob == "" || view.ID == "" {
		t.Fatalf("incomplete capture: %+v", view)
	}
	return fact.Entry.ID, view
}
