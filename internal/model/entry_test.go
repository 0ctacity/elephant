package model

import (
	"errors"
	"testing"
	"time"
)

func TestEntryValidationAndLifecycle(t *testing.T) {
	for _, k := range []Kind{Fact, Decision, Task} {
		e := Entry{Kind: k, Title: "Useful title", Body: "Enough context", Status: DefaultStatus(k)}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		e.Status = "nonsense"
		if !errors.Is(e.Validate(), ErrInvalidStatus) {
			t.Fatal("invalid status accepted")
		}
		e.Status = DefaultStatus(k)
		e.Title = " "
		if e.Validate() == nil {
			t.Fatal("empty title accepted")
		}
	}
	if !errors.Is((Entry{Kind: "note", Title: "a", Body: "b"}).Validate(), ErrInvalidKind) {
		t.Fatal("invalid kind accepted")
	}
	for _, pair := range [][3]string{{"task", "done", "active"}, {"task", "cancelled", "open"}, {"decision", "superseded", "active"}, {"fact", "retired", "active"}} {
		if !errors.Is(ValidateTransition(Kind(pair[0]), pair[1], pair[2]), ErrInvalidTransition) {
			t.Fatalf("accepted %v", pair)
		}
	}
	if err := ValidateTransition(Task, "blocked", "active"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTransition(Fact, "stale", "active"); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := Evidence{ID: "evidence-one", EntryID: "fact-one", Path: "src/main.go", Line: 12, Commit: "abc123", Blob: "deadbeef", CreatedAt: now, VerifiedAt: now}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	blank := valid
	blank.Line = 0
	if err := blank.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Evidence){
		"missing ID":     func(e *Evidence) { e.ID = "" },
		"missing entry":  func(e *Evidence) { e.EntryID = "" },
		"absolute path":  func(e *Evidence) { e.Path = "/etc/passwd" },
		"traversal":      func(e *Evidence) { e.Path = "../secret" },
		"negative line":  func(e *Evidence) { e.Line = -1 },
		"large line":     func(e *Evidence) { e.Line = 1_000_001 },
		"spaced commit":  func(e *Evidence) { e.Commit = "abc 123" },
		"zero created":   func(e *Evidence) { e.CreatedAt = time.Time{} },
		"early verified": func(e *Evidence) { e.VerifiedAt = now.Add(-time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			e := valid
			mutate(&e)
			if e.Validate() == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	if _, err := CleanEvidenceLine(-1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative line: %v", err)
	}
	if _, err := CleanEvidenceLine(0); err != nil {
		t.Fatalf("zero line: %v", err)
	}
	for _, state := range []string{EvidenceUnchanged, EvidenceChanged, EvidenceMissing, EvidenceUnavailable} {
		if !ValidEvidenceState(state) {
			t.Fatalf("rejected state %q", state)
		}
	}
	if ValidEvidenceState("retired") {
		t.Fatal("accepted unknown state")
	}
}

func TestFilePathsAndRelationKinds(t *testing.T) {
	for _, p := range []string{"/etc/passwd", "../secret", "a/../../secret", "C:\\secret", "a\\b", "", "."} {
		if _, err := CleanFile(p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if p, err := CleanFile("internal/./runtime.go"); err != nil || p != "internal/runtime.go" {
		t.Fatalf("%s %v", p, err)
	}
	for _, r := range []struct {
		a    Kind
		edge string
		b    Kind
	}{{Decision, "supersedes", Decision}, {Task, "depends_on", Task}, {Task, "implements", Decision}, {Fact, "supports", Decision}} {
		if err := ValidateRelation(r.a, r.edge, r.b); err != nil {
			t.Fatal(err)
		}
	}
	if ValidateRelation(Fact, "depends_on", Task) == nil {
		t.Fatal("invalid relation accepted")
	}
}
