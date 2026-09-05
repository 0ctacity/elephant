package model

import (
	"errors"
	"testing"
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
