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

func TestRecallDistinguishesEvidenceStates(t *testing.T) {
	ctx := context.Background()
	cwd := evidenceRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "recall-evidence.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	steadyID, _ := attachFact(t, s, cwd, "runtime.go", 0)
	if err = os.WriteFile(filepath.Join(cwd, "runtime.go"), []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	extraFact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Extra", Body: "Context"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddEvidence(ctx, cwd, extraFact.Entry.ID, "runtime.go", 0); err != nil {
		t.Fatal(err)
	}
	packet, err := s.Recall(ctx, cwd, "")
	if err != nil || len(packet.Evidence) != 2 {
		t.Fatalf("%+v %v", packet, err)
	}
	states := map[string]int{}
	for _, view := range packet.Evidence {
		states[view.State]++
		if view.EntryID != steadyID && view.EntryID != extraFact.Entry.ID {
			t.Fatalf("unexpected entry: %+v", view)
		}
	}
	if states[model.EvidenceChanged] != 1 || states[model.EvidenceUnchanged] != 1 {
		t.Fatalf("states=%v", states)
	}
	if packet.Evidence[1].Commit == "" || packet.Evidence[1].Blob == "" {
		t.Fatalf("recall dropped evidence metadata: %+v", packet.Evidence[1])
	}
}
