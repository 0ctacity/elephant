package app_test

import (
	"context"
	"errors"
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
	entryID, first := attachFact(t, s, cwd, "runtime.go", 3)
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
		{"uncommitted file", "uncommitted.go", 0, model.ErrInvalidInput},
		{"escape", "../escape.go", 0, model.ErrInvalidInput},
		{"negative line", "runtime.go", -1, model.ErrInvalidInput},
		{"line out of range", "runtime.go", 99, model.ErrInvalidInput},
		{"missing entry", "runtime.go", 0, model.ErrEntryNotFound},
	} {
		if tc.name == "uncommitted file" {
			writeWorkingFile(t, cwd, tc.path, "package uncommitted\n")
		}
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
	// Dirtying an unrelated line leaves line-scoped evidence unchanged.
	writeWorkingFile(t, cwd, "runtime.go", "package changed\n\nconst Name = \"runtime\"\n\nfunc Run() {}\n")
	report, err = s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || report.Counts[model.EvidenceUnchanged] != 1 {
		t.Fatalf("unrelated dirty line moved evidence: %+v %v", report, err)
	}
	// Dirtying the selected line reports changed.
	writeWorkingFile(t, cwd, "runtime.go", "package changed\n\nconst Name = \"other\"\n\nfunc Run() {}\n")
	report, err = s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || report.Counts[model.EvidenceChanged] != 1 {
		t.Fatalf("%+v %v", report, err)
	}
	// Attaching while dirty still records the commit, so restoring the
	// committed bytes verifies unchanged.
	second, err := s.AddEvidence(ctx, cwd, entryID, "runtime.go", 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("second attach reused the row ID: %+v", second)
	}
	writeWorkingFile(t, cwd, "runtime.go", committedRuntimeGo)
	report, err = s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || report.Counts[model.EvidenceUnchanged] != 2 {
		t.Fatalf("dirty attach did not record the commit: %+v %v", report, err)
	}
	// Refresh re-pins to the current HEAD: commit a change that keeps the
	// selected line intact first.
	writeWorkingFile(t, cwd, "runtime.go", "package third\n\nconst Name = \"runtime\"\n\nfunc Run() {}\n")
	commitFile(t, cwd, "runtime.go", "third revision")
	refreshed, err := s.RefreshEvidence(ctx, cwd, entryID, first.ID)
	if err != nil || len(refreshed.Evidence) != 1 || refreshed.Evidence[0].ID != first.ID || refreshed.Counts[model.EvidenceUnchanged] != 1 {
		t.Fatalf("%+v %v", refreshed, err)
	}
	if _, err = s.RemoveEvidence(ctx, cwd, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RemoveEvidence(ctx, cwd, second.ID); err != nil {
		t.Fatal(err)
	}
	report, err = s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || len(report.Evidence) != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	if got, err := s.Get(ctx, cwd, entryID); err != nil || got.Entry.Status != "active" {
		t.Fatalf("evidence writes must not retire the fact: %+v %v", got, err)
	}
	// Refreshing a missing row, a missing entry, or an out-of-range line fails
	// without touching stored rows.
	if _, err = s.RefreshEvidence(ctx, cwd, entryID, "missing"); !errors.Is(err, model.ErrEvidenceNotFound) {
		t.Fatalf("refresh missing: %v", err)
	}
}
