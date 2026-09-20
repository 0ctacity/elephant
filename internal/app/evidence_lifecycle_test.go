package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func TestEvidenceAttachVerifyRefreshAndRemove(t *testing.T) {
	ctx := context.Background()
	cwd := evidenceRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "evidence.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	entryID, first := attachFact(t, s, cwd, "runtime.go", 1)
	report, err := s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || len(report.Evidence) != 1 || report.Counts[model.EvidenceUnchanged] != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	listed, err := s.ListEvidence(ctx, cwd, entryID)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("%+v %v", listed, err)
	}
	for _, tc := range []struct {
		name, path string
		line       int
		want       error
	}{
		{"missing file", "absent.go", 0, model.ErrInvalidInput},
		{"escape", "../escape.go", 0, model.ErrInvalidInput},
		{"negative line", "runtime.go", -1, model.ErrInvalidInput},
		{"missing entry", "runtime.go", 0, model.ErrEntryNotFound},
	} {
		target := entryID
		if tc.name == "missing entry" {
			target = "missing"
		}
		if _, err = s.AddEvidence(ctx, cwd, target, tc.path, tc.line); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	decision, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Decision", Body: "Reason"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddEvidence(ctx, cwd, decision.Entry.ID, "runtime.go", 0); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("decision evidence: %v", err)
	}
	if err = os.WriteFile(filepath.Join(cwd, "runtime.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report, err = s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || report.Counts[model.EvidenceChanged] != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	second, err := s.AddEvidence(ctx, cwd, entryID, "runtime.go", 0)
	if err != nil || second.State != model.EvidenceUnchanged || second.ID == first.ID {
		t.Fatalf("%+v %v", second, err)
	}
	if err = os.WriteFile(filepath.Join(cwd, "runtime.go"), []byte("package third\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.RefreshEvidence(ctx, cwd, entryID, first.ID)
	if err != nil || len(refreshed.Evidence) != 1 || refreshed.Evidence[0].ID != first.ID || refreshed.Counts[model.EvidenceUnchanged] != 1 {
		t.Fatalf("%+v %v", refreshed, err)
	}
}
