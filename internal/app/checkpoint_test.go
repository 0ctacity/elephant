package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func TestCheckpointLifecycleRecallAndIsolation(t *testing.T) {
	ctx := context.Background()
	cwd := repo(t)
	other := repo(t)
	path := filepath.Join(t.TempDir(), "checkpoint.zova")
	db, err := zova.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := app.New(db)
	if _, err = s.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: ""}); err == nil {
		t.Fatal("empty summary accepted")
	}
	oversized := strings.Repeat("x", 2001)
	if _, err = s.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: oversized}); err == nil {
		t.Fatal("oversized summary accepted")
	}
	fact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "F", Body: "B"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AddCheckpoint(ctx, cwd, app.CheckpointInput{
		Summary: "Implemented authentication", Next: "Add integration tests",
		RelatedFiles: []string{"internal/auth.go"},
		Relations:    []app.RelationInput{{Type: "relates", EntryID: fact.Entry.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Checkpoint.ActorID == "" || first.Checkpoint.EndCommit == nil || len(first.Relations) != 2 {
		t.Fatalf("%+v", first)
	}
	// Atomicity: bad relation must roll back the checkpoint.
	before, err := s.ListCheckpoints(ctx, cwd, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: "Bad", Relations: []app.RelationInput{{Type: "relates", EntryID: "missing"}}}); err == nil {
		t.Fatal("invalid checkpoint relation accepted")
	}
	after, err := s.ListCheckpoints(ctx, cwd, 10, 0)
	if err != nil || len(after) != len(before) {
		t.Fatal(after, err)
	}
	second, err := s.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: "Second", Completed: "Did X", Commands: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if second.Checkpoint.StartCommit == nil || first.Checkpoint.EndCommit == nil || *second.Checkpoint.StartCommit != *first.Checkpoint.EndCommit {
		t.Fatalf("start commit should chain: %+v %+v", first.Checkpoint, second.Checkpoint)
	}
	list, err := s.ListCheckpoints(ctx, cwd, 10, 0)
	if err != nil || len(list) != 2 || list[0].ID != second.Checkpoint.ID {
		t.Fatal(list, err)
	}
	packet, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if packet.Checkpoint == nil || packet.Checkpoint.Checkpoint.ID != second.Checkpoint.ID {
		t.Fatalf("%+v", packet.Checkpoint)
	}
	// Isolation: other project sees no checkpoints.
	none, err := s.ListCheckpoints(ctx, other, 10, 0)
	if err != nil || len(none) != 0 {
		t.Fatal(none, err)
	}
	otherPacket, err := s.Recall(ctx, other, "")
	if err != nil || otherPacket.Checkpoint != nil {
		t.Fatal(otherPacket, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := zova.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2 := app.New(db2)
	restarted, err := s2.ListCheckpoints(ctx, cwd, 10, 0)
	if err != nil || len(restarted) != 2 {
		t.Fatal(restarted, err)
	}
	if _, err = s2.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: strings.Repeat("y", 32769), Completed: strings.Repeat("y", 40000)}); err == nil {
		// Summary length is the binding constraint here; completed oversize must also fail.
		t.Fatal("oversized field accepted")
	}
	_ = errors.Is(err, model.ErrInvalidInput)
}
