package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

// committedRuntimeGo is the committed content every evidence test starts
// from. Attach and refresh always read from a commit, so the file must exist
// at HEAD for capture to succeed.
const committedRuntimeGo = "package runtime\n\nconst Name = \"runtime\"\n\nfunc Run() {}\n"

func evidenceRepo(t *testing.T) string {
	t.Helper()
	cwd := repo(t)
	if err := os.WriteFile(filepath.Join(cwd, "runtime.go"), []byte(committedRuntimeGo), 0600); err != nil {
		t.Fatal(err)
	}
	commitFile(t, cwd, "runtime.go", "add runtime evidence fixture")
	return cwd
}

func commitFile(t *testing.T, cwd, file, message string) string {
	t.Helper()
	for _, args := range [][]string{
		{"-C", cwd, "add", "--", file},
		{"-C", cwd, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-qm", message},
		{"-C", cwd, "rev-parse", "HEAD"},
	} {
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		if args[0] == "rev-parse" {
			return string(out[:len(out)-1])
		}
	}
	return ""
}

func writeWorkingFile(t *testing.T, cwd, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cwd, file), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
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
