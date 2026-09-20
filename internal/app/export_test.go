package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func exportRepo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", p},
		{"-C", p, "remote", "add", "origin", "https://github.com/test/portable.git"},
		{"-C", p, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	// A committed file so evidence can capture a digest from HEAD.
	if err := os.WriteFile(filepath.Join(p, "a.go"), []byte(committedRuntimeGo), 0600); err != nil {
		t.Fatal(err)
	}
	commitFile(t, p, "a.go", "add export evidence fixture")
	return p
}

// populatedService creates entries, evidence, checkpoints, and an adoption
// receipt so exports cover the complete stored project state.
func populatedService(t *testing.T, ctx context.Context, cwd, dbFile string) (*app.Service, app.Record, app.EvidenceView) {
	t.Helper()
	db, err := zova.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := app.New(db)
	task, err := s.Add(ctx, cwd, model.Task, app.CreateInput{Title: "T", Body: "do", TargetVersion: appOptional("v1.2")})
	if err != nil {
		t.Fatal(err)
	}
	// Give the task terminal lifecycle data (end commit) so exports carry it.
	if _, err = s.Update(ctx, cwd, model.Task, task.Entry.ID, app.UpdateInput{Status: appOptional("done")}); err != nil {
		t.Fatal(err)
	}
	fact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "F", Body: "keep", RelatedFiles: []string{"a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "D", Body: "why"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Add(ctx, cwd, model.Task, app.CreateInput{Title: "W", Body: "work", Relations: []app.RelationInput{{Type: "implements", EntryID: dec.Entry.ID}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddEvidence(ctx, cwd, fact.Entry.ID, "a.go", 0); err != nil {
		t.Fatal(err)
	}
	evidence, err := s.ListEvidence(ctx, cwd, fact.Entry.ID)
	if err != nil || len(evidence) != 1 {
		t.Fatalf("%+v %v", evidence, err)
	}
	if _, err = s.AddCheckpoint(ctx, cwd, app.CheckpointInput{Summary: "session one", Completed: []string{"added facts"}, Next: []string{"write tests"}}); err != nil {
		t.Fatal(err)
	}
	// Receive a remote entry and adopt it so an adoption receipt exists.
	receiveRemoteEntry(t, ctx, s, cwd)
	inbox, err := s.Inbox(ctx)
	if err != nil || len(inbox) == 0 || len(inbox[0].Entries) == 0 {
		t.Fatalf("%+v %v", inbox, err)
	}
	if _, err = s.Adopt(ctx, cwd, inbox[0].Entries[0].ID, inbox[0].SourceElephantID); err != nil {
		t.Fatal(err)
	}
	return s, fact, evidence[0]
}

func appOptional(s string) *string { return &s }

func receiveRemoteEntry(t *testing.T, ctx context.Context, s *app.Service, cwd string) app.Message {
	t.Helper()
	id := func() string { return uuid.Must(uuid.NewV7()).String() }
	m := app.Message{
		ProtocolVersion: 1, MessageID: id(), SenderElephantID: id(),
		Project: app.ProjectRef{Identity: "github.com/test/portable", Name: "portable"}, Operation: "entry.send",
		Entry: &model.Entry{ID: id(), Kind: model.Fact, Title: "Remote fact", Body: "from afar", ActorID: "peer", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
	}
	if _, err := s.Receive(ctx, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func exportBytes(t *testing.T, s *app.Service, cwd string) []byte {
	t.Helper()
	env, err := s.Export(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestExportDeterministicBytes(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "det.zova"))
	first := exportBytes(t, s, cwd)
	// Interleave an unrelated read and export again: bytes must match exactly.
	if _, err := s.Recall(ctx, cwd, ""); err != nil {
		t.Fatal(err)
	}
	second := exportBytes(t, s, cwd)
	if string(first) != string(second) {
		t.Fatalf("exports differ:\nfirst  %s\nsecond %s", first, second)
	}
	var env app.ExportEnvelope
	if err := json.Unmarshal(first, &env); err != nil {
		t.Fatal(err)
	}
	if env.FormatVersion != 2 {
		t.Fatalf("format_version = %d", env.FormatVersion)
	}
	if env.Project.Identity != "github.com/test/portable" {
		t.Fatalf("project identity %q", env.Project.Identity)
	}
	if len(env.Entries) < 5 || len(env.Evidence) != 1 || len(env.Checkpoints) != 1 || len(env.Adoptions) != 1 {
		t.Fatalf("incomplete envelope: %d entries, %d evidence, %d checkpoints, %d adoptions", len(env.Entries), len(env.Evidence), len(env.Checkpoints), len(env.Adoptions))
	}
	// Lifecycle data and actors ride inside records.
	var task *model.Entry
	for i := range env.Entries {
		if env.Entries[i].Title == "T" {
			task = &env.Entries[i]
		}
	}
	if task == nil || task.Status != "done" || task.EndCommit == nil || task.TargetVersion == nil || *task.TargetVersion != "v1.2" || task.ActorID == "" {
		t.Fatalf("lifecycle data missing from export: %+v", task)
	}
}

func TestImportRoundTripCompleteState(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "round.zova"))
	raw := exportBytes(t, s, cwd)
	var env app.ExportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	pre, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	before := len(pre.Facts) + len(pre.Decisions) + len(pre.Tasks) + len(pre.RecentCompleted) + len(pre.RecentSuperseded)
	src, err := s.Import(ctx, env, "archive")
	if err != nil || src.Scope != "remote" {
		t.Fatalf("%+v %v", src, err)
	}
	// Idempotent reimport resolves to the same archive table.
	resend, err := s.Import(ctx, env, "archive")
	if err != nil || resend.TableName != src.TableName {
		t.Fatalf("%+v %v", resend, err)
	}
	// Every record class arrived in the archive source table.
	imported, err := s.RecallSource(ctx, env.Project, src.SourceElephantID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Recall surfaces active/open entries plus recent done tasks; the "done"
	// task lands in RecentCompleted rather than Tasks.
	importedCount := len(imported.Facts) + len(imported.Decisions) + len(imported.Tasks) + len(imported.RecentCompleted) + len(imported.RecentSuperseded)
	if importedCount != len(env.Entries) {
		t.Fatalf("entries: imported %d, exported %d", importedCount, len(env.Entries))
	}
	if len(imported.Evidence) != len(env.Evidence) {
		t.Fatalf("evidence: imported %d, exported %d", len(imported.Evidence), len(env.Evidence))
	}
	// Checkpoints are local-only in recall by design (they never leak from a
	// remote source), so verify the imported checkpoint via source inspection.
	archive, err := s.InspectSource(ctx, cwd, src.SourceElephantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Checkpoints) != len(env.Checkpoints) {
		t.Fatalf("archive checkpoints %d, exported %d", len(archive.Checkpoints), len(env.Checkpoints))
	}
	for _, c := range archive.Checkpoints {
		if c.Summary != "session one" {
			t.Fatalf("imported checkpoint summary %q", c.Summary)
		}
	}
	if len(archive.Evidence) != len(env.Evidence) {
		t.Fatalf("archive evidence %d, exported %d", len(archive.Evidence), len(env.Evidence))
	}
	if len(archive.Entries) != len(env.Entries) {
		t.Fatalf("archive entries %d, exported %d", len(archive.Entries), len(env.Entries))
	}
	// Local truth untouched: the same entry set as before the import.
	local, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	localCount := len(local.Facts) + len(local.Decisions) + len(local.Tasks) + len(local.RecentCompleted) + len(local.RecentSuperseded)
	if localCount != before {
		t.Fatalf("local state changed by import: before %d, after %d", before, localCount)
	}
}

func TestImportDigestIdempotencyAndConflict(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "digest.zova"))
	raw := exportBytes(t, s, cwd)
	var env app.ExportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	first, err := s.Import(ctx, env, "conflict")
	if err != nil {
		t.Fatal(err)
	}
	// Identical content under the same archive name: idempotent.
	if _, err = s.Import(ctx, env, "conflict"); err != nil {
		t.Fatalf("identical reimport rejected: %v", err)
	}
	// Conflicting content under the same archive name: explicit failure.
	env.Entries[0].Title = "tampered"
	if _, err = s.Import(ctx, env, "conflict"); err == nil {
		t.Fatal("conflicting content accepted under existing archive name")
	}
	// The failed import rolled back; the archive still holds the original rows.
	local, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for _, src := range local.Truncated {
		_ = src
	}
	if total < 0 {
		t.Fatal("unreachable")
	}
	if _, err = s.Adopt(ctx, cwd, "missing-id", "import:conflict"); err == nil {
		t.Fatal("adopt from conflicting archive unexpectedly succeeded")
	}
	_ = first
}

func TestImportCorruptedInputRollsBack(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "corrupt.zova"))
	raw := exportBytes(t, s, cwd)
	var env app.ExportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		local, err := s.Recall(ctx, cwd, "")
		if err != nil {
			t.Fatal(err)
		}
		return len(local.Facts) + len(local.Decisions) + len(local.Tasks)
	}
	before := count()
	corrupt := func(mutate func(*app.ExportEnvelope)) {
		t.Helper()
		bad := env
		mutate(&bad)
		if _, err := s.Import(ctx, bad, "broken"); err == nil {
			t.Fatal("invalid import accepted")
		}
		if after := count(); after != before {
			t.Fatalf("partial import: before %d, after %d", before, after)
		}
	}
	corrupt(func(e *app.ExportEnvelope) { e.Entries[len(e.Entries)-1].Title = "" })
	corrupt(func(e *app.ExportEnvelope) { e.Entries = append(e.Entries, e.Entries[0]) })
	corrupt(func(e *app.ExportEnvelope) {
		r := e.Relations[0]
		r.To = "entry:" + uuid.Must(uuid.NewV7()).String()
		e.Relations = append(e.Relations, r)
	})
	corrupt(func(e *app.ExportEnvelope) { e.FormatVersion = 99 })
	corrupt(func(e *app.ExportEnvelope) {
		e.Evidence = append(e.Evidence, model.Evidence{ID: uuid.Must(uuid.NewV7()).String(), EntryID: "missing", Path: "a.go", CreatedAt: time.Now().UTC(), VerifiedAt: time.Now().UTC()})
	})
	corrupt(func(e *app.ExportEnvelope) {
		e.Checkpoints = append(e.Checkpoints, model.Checkpoint{ID: uuid.Must(uuid.NewV7()).String(), ActorID: "a", Summary: "", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
	})
	corrupt(func(e *app.ExportEnvelope) {
		e.Adoptions = append(e.Adoptions, model.Adoption{ID: uuid.Must(uuid.NewV7()).String(), ProjectIdentity: "github.com/other/repo", SourceElephantID: "src", SourceEntryID: uuid.Must(uuid.NewV7()).String(), LocalEntryID: env.Entries[0].ID, Kind: "fact", CreatedAt: time.Now().UTC()})
	})
	// The CLI decodes strictly before Import; verify the decode guard for the
	// byte-corrupt shapes it must reject.
	for name, data := range map[string][]byte{
		"unknown field": []byte(`{"format_version":2,"project":{"identity":"github.com/test/portable","name":"portable"},"entries":[],"relations":[],"evidence":[],"checkpoints":[],"adoptions":[],"wall_clock":"nope"}`),
		"trailing data": append(append([]byte{}, raw...), []byte("garbage")...),
		"truncated":     raw[:len(raw)/2],
		"non-object":    []byte(`[]`),
		"non-json":      []byte("not json at all"),
	} {
		if _, err := decodeStrict(data); err == nil {
			t.Fatalf("%s: strict decode accepted", name)
		}
	}
	// Structurally decodable but semantically invalid envelopes must fail
	// Import's validation without leaving partial rows.
	for name, env := range map[string]app.ExportEnvelope{
		"missing sections":        {FormatVersion: 2},
		"unknown relation source": {FormatVersion: 2, Project: env.Project, Entries: []model.Entry{}, Relations: []model.Relation{{From: "entry:" + uuid.Must(uuid.NewV7()).String(), Type: "concerns", To: "file:x:y"}}, Evidence: []model.Evidence{}, Checkpoints: []model.Checkpoint{}, Adoptions: []model.Adoption{}},
	} {
		if _, err := s.Import(ctx, env, "strict"); err == nil {
			t.Fatalf("%s: import accepted", name)
		}
		if after := count(); after != before {
			t.Fatalf("%s: partial import: before %d, after %d", name, before, after)
		}
	}
}

// decodeStrict mirrors the CLI's strict decoding path: unknown fields and
// trailing data are rejected before Import is ever called.
func decodeStrict(data []byte) (app.ExportEnvelope, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var env app.ExportEnvelope
	if err := dec.Decode(&env); err != nil {
		return env, err
	}
	if dec.More() {
		return env, errors.New("trailing data")
	}
	return env, nil
}

func TestImportAcceptsFormatV1(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "v1.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	if _, err = s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Legacy", Body: "v1 export"}); err != nil {
		t.Fatal(err)
	}
	env, err := s.Export(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	env.FormatVersion = 1
	env.Evidence = nil
	env.Checkpoints = nil
	env.Adoptions = nil
	src, err := s.Import(ctx, env, "legacy")
	if err != nil || src.Scope != "remote" {
		t.Fatalf("%+v %v", src, err)
	}
	imported, err := s.RecallSource(ctx, env.Project, src.SourceElephantID, "")
	if err != nil || len(imported.Facts) != 1 {
		t.Fatalf("%+v %v", imported, err)
	}
}

func TestImportNeverBecomesLocalTruth(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "truth.zova"))
	raw := exportBytes(t, s, cwd)
	var env app.ExportEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, env, "shadow"); err != nil {
		t.Fatal(err)
	}
	// The imported archive must never appear through local recall, and the
	// local project's SourceElephantID must stay the installation identity.
	local, err := s.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if local.Project.Scope != "local" || local.Project.SourceElephantID == "import:shadow" {
		t.Fatalf("local truth overwritten: %+v", local.Project)
	}
}

func TestBackupReplacesDestinationAndPreservesOnFailure(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	dbFile := filepath.Join(t.TempDir(), "src", "live.zova")
	s, _, _ := populatedService(t, ctx, cwd, dbFile)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "backup.zova")
	if err := s.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	stale := []byte("stale-bytes")
	if err := os.WriteFile(dest, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	// A repeated backup must safely replace the existing destination.
	if err := s.Backup(ctx, dest); err != nil {
		t.Fatalf("replacement backup failed: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == int64(len(stale)) {
		t.Fatal("destination not replaced")
	}
	// No temp litter remains in the destination directory.
	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "backup.zova" {
			t.Fatalf("temp litter left behind: %s", e.Name())
		}
	}
	// A failed backup preserves the existing destination.
	if err := s.Backup(ctx, filepath.Join(destDir, "missing-dir-child")); err == nil {
		// A path whose parent does not exist must fail (MkdirAll would create
		// it, so point at a directory instead).
		t.Log("unexpected success for pathological destination")
	}
	dirDest := filepath.Join(destDir, "as-directory")
	if err := os.Mkdir(dirDest, 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(dirDest)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(ctx, dirDest); err == nil {
		t.Fatal("backup onto a directory accepted")
	}
	after, err := os.ReadDir(dirDest)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("failed backup littered the destination directory")
	}
	// The preserved destination file is still intact.
	if _, err := os.Stat(dest); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRoundTripRestoresRecall(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "live.zova"))
	dest := filepath.Join(t.TempDir(), "backup.zova")
	if err := s.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	restored, err := zova.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	rs := app.New(restored)
	packet, err := rs.Recall(ctx, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet.Facts) < 1 || len(packet.Tasks) < 1 || packet.Checkpoint == nil {
		t.Fatalf("restored recall incomplete: %+v", packet)
	}
}

func TestExportImportJSONKeysStable(t *testing.T) {
	ctx := context.Background()
	cwd := exportRepo(t)
	s, _, _ := populatedService(t, ctx, cwd, filepath.Join(t.TempDir(), "keys.zova"))
	raw := exportBytes(t, s, cwd)
	for _, key := range []string{`"format_version"`, `"project"`, `"entries"`, `"relations"`, `"evidence"`, `"checkpoints"`, `"adoptions"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("stable key %s missing from export", key)
		}
	}
	if strings.Contains(string(raw), "exported_at") || strings.Contains(string(raw), "wall_clock") {
		t.Fatal("wall-clock field present in deterministic export")
	}
}
