package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage"
	"elephant/internal/storage/zova"
)

func TestEvidenceRemovalAndMissingStates(t *testing.T) {
	ctx := context.Background()
	cwd := evidenceRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "missing.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	entryID, second := attachFact(t, s, cwd, "runtime.go", 0)
	if _, err = s.RefreshEvidence(ctx, cwd, entryID, "missing"); !errors.Is(err, model.ErrEvidenceNotFound) {
		t.Fatalf("refresh missing: %v", err)
	}
	if _, err = s.RefreshEvidence(ctx, cwd, "missing", ""); !errors.Is(err, model.ErrEntryNotFound) {
		t.Fatalf("refresh missing entry: %v", err)
	}
	if _, err = s.RemoveEvidence(ctx, cwd, "missing"); !errors.Is(err, model.ErrEvidenceNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if _, err = s.RemoveEvidence(ctx, cwd, second.ID); err != nil {
		t.Fatal(err)
	}
	report, err := s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || len(report.Evidence) != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	if got, err := s.Get(ctx, cwd, entryID); err != nil || got.Entry.Status != "active" {
		t.Fatalf("verification must not retire the fact: %+v %v", got, err)
	}
	if err = os.Remove(filepath.Join(cwd, "runtime.go")); err != nil {
		t.Fatal(err)
	}
	extraFact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Extra", Body: "Context"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddEvidence(ctx, cwd, extraFact.Entry.ID, "runtime.go", 0); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("attaching to a missing file: %v", err)
	}
}

func TestEvidenceReadOnlyVerificationAndRollback(t *testing.T) {
	ctx := context.Background()
	cwd := evidenceRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "rollback.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	entryID, _ := attachFact(t, s, cwd, "runtime.go", 0)
	before, err := s.Get(ctx, cwd, entryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyEvidence(ctx, cwd, entryID); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, cwd, entryID)
	if err != nil || !after.Entry.UpdatedAt.Equal(before.Entry.UpdatedAt) || after.Entry.Status != before.Entry.Status {
		t.Fatalf("verify mutated the fact: %+v %+v", before.Entry, after.Entry)
	}
	status, err := s.Status(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transact(ctx, func(tx storage.Tx) error {
		if err := tx.PutEvidence(status.Project, model.Evidence{ID: "doomed", EntryID: entryID, Path: "runtime.go"}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	report, err := s.VerifyEvidence(ctx, cwd, entryID)
	if err != nil || len(report.Evidence) != 1 {
		t.Fatalf("rolled-back evidence survived: %+v %v", report, err)
	}
}
