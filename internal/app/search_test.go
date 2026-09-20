package app_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"elephant/internal/app"
	"elephant/internal/model"
	"elephant/internal/storage/zova"
)

func searchRepo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", p},
		{"-C", p, "remote", "add", "origin", "https://github.com/test/search.git"},
		{"-C", p, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "--allow-empty", "-qm", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v", out, err)
		}
	}
	return p
}

func TestSearchRelatedHistory(t *testing.T) {
	ctx := context.Background()
	cwd := searchRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "search.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	t.Setenv("ELEPHANT_ACTOR_ID", "searcher")
	fact, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Shutdown ownership", Body: "Workers acknowledge cancellation", RelatedFiles: []string{"internal/runtime/worker.go"}})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Own cancellation", Body: "Move lifecycle into runtime"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Add(ctx, cwd, model.Task, app.CreateInput{Title: "Verify shutdown", Body: "Run integration suite", Relations: []app.RelationInput{{Type: "implements", EntryID: dec.Entry.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(ctx, cwd, app.SearchFilters{Query: "shutdown ownership"})
	if err != nil || len(res) == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if res[0].Match == nil {
		t.Fatal("missing match metadata")
	}
	byActor, err := s.Search(ctx, cwd, app.SearchFilters{Actor: "searcher"})
	if err != nil || len(byActor) != 3 {
		t.Fatalf("%+v %v", byActor, err)
	}
	byFile, err := s.Search(ctx, cwd, app.SearchFilters{File: "internal/runtime/worker.go"})
	if err != nil || len(byFile) != 1 || byFile[0].Entry.ID != fact.Entry.ID {
		t.Fatalf("%+v %v", byFile, err)
	}
	byKind, err := s.Search(ctx, cwd, app.SearchFilters{Kind: model.Task, Query: "shutdown"})
	if err != nil || len(byKind) != 1 {
		t.Fatalf("%+v %v", byKind, err)
	}
	hist, err := s.History(ctx, cwd, "internal/runtime/worker.go", 20)
	if err != nil || len(hist) != 1 || hist[0].ID != fact.Entry.ID {
		t.Fatalf("%+v %v", hist, err)
	}
	rel, err := s.Related(ctx, cwd, task.Entry.ID, "outgoing", "", 1, 20)
	if err != nil || len(rel) != 1 || rel[0].Entry == nil || rel[0].Entry.ID != dec.Entry.ID {
		t.Fatalf("%+v %v", rel, err)
	}
	back, err := s.Related(ctx, cwd, dec.Entry.ID, "incoming", "implements", 1, 20)
	if err != nil || len(back) != 1 {
		t.Fatalf("%+v %v", back, err)
	}
	if _, err = s.Related(ctx, cwd, task.Entry.ID, "sideways", "", 1, 20); err == nil {
		t.Fatal("bad direction accepted")
	}
	if _, err = s.Related(ctx, cwd, task.Entry.ID, "both", "", 9, 20); err == nil {
		t.Fatal("unbounded depth accepted")
	}
}

func TestSearchPerformance(t *testing.T) {
	ctx := context.Background()
	cwd := searchRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "perf.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	for i := range 300 {
		kind := model.Fact
		if i%3 == 1 {
			kind = model.Decision
		} else if i%3 == 2 {
			kind = model.Task
		}
		if _, err = s.Add(ctx, cwd, kind, app.CreateInput{Title: fmt.Sprintf("Entry %03d shutdown", i), Body: "context"}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	res, err := s.Search(ctx, cwd, app.SearchFilters{Query: "shutdown", Limit: 20})
	if err != nil || len(res) != 20 {
		t.Fatalf("%+v %v", res, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("search too slow: %v", elapsed)
	}
}

func TestSearchFileFilterBeforePagination(t *testing.T) {
	ctx := context.Background()
	cwd := searchRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "filefilter.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	// 30 matching-text entries; only the 20 oldest carry the file relation,
	// so a pre-filtered SQL page would return zero visible matches.
	var withFile []app.SearchResult
	for i := 0; i < 30; i++ {
		files := []string(nil)
		if i < 20 {
			files = []string{"pkg/old.go"}
		}
		r, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: fmt.Sprintf("Paginated %03d", i), Body: "shared needle", RelatedFiles: files})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 1 {
			withFile = append(withFile, app.SearchResult{Entry: r.Entry})
		}
	}
	// Search returns newest first; withFile was appended oldest first.
	for i, j := 0, len(withFile)-1; i < j; i, j = i+1, j-1 {
		withFile[i], withFile[j] = withFile[j], withFile[i]
	}
	// All 20 matches survive pagination on page one.
	res, err := s.Search(ctx, cwd, app.SearchFilters{Query: "needle", File: "pkg/old.go", Limit: 20})
	if err != nil || len(res) != 20 {
		t.Fatalf("len=%d err=%v", len(res), err)
	}
	for i, r := range res {
		if r.Entry.ID != withFile[i].Entry.ID {
			t.Fatalf("order mismatch at %d", i)
		}
		if r.Match == nil || !contains(r.Match, "file") || !contains(r.Match, "body") {
			t.Fatalf("unexpected match metadata: %v", r.Match)
		}
	}
	// Offset skips matching entries, not raw rows.
	page2, err := s.Search(ctx, cwd, app.SearchFilters{Query: "needle", File: "pkg/old.go", Limit: 5, Offset: 20})
	if err != nil || len(page2) != 0 {
		t.Fatalf("offset past end: len=%d err=%v", len(page2), err)
	}
	// Page through a sparse stream: newer entries (with the file) are created
	// after the change below, so exercise offset against the filtered order.
	more, err := s.Search(ctx, cwd, app.SearchFilters{Query: "needle", File: "pkg/old.go", Limit: 3, Offset: 7})
	if err != nil || len(more) != 3 || more[0].Entry.ID != withFile[7].Entry.ID || more[2].Entry.ID != withFile[9].Entry.ID {
		t.Fatalf("offset window mismatch: %+v %v", more, err)
	}
}

func TestSearchUpdatedTimeFilters(t *testing.T) {
	ctx := context.Background()
	cwd := searchRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "timefilter.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	old, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "Old fact", Body: "time filter"})
	if err != nil {
		t.Fatal(err)
	}
	new, err := s.Add(ctx, cwd, model.Fact, app.CreateInput{Title: "New fact", Body: "time filter"})
	if err != nil {
		t.Fatal(err)
	}
	if !new.Entry.UpdatedAt.After(old.Entry.UpdatedAt) {
		t.Fatalf("timestamps equal: %v vs %v", old.Entry.UpdatedAt, new.Entry.UpdatedAt)
	}
	between := old.Entry.UpdatedAt.Add(time.Duration(new.Entry.UpdatedAt.Sub(old.Entry.UpdatedAt)/2) + time.Nanosecond)
	after, err := s.Search(ctx, cwd, app.SearchFilters{Query: "time filter", UpdatedAfter: &between})
	if err != nil || len(after) != 1 || after[0].Entry.ID != new.Entry.ID {
		t.Fatalf("updated-after: %+v %v", after, err)
	}
	before, err := s.Search(ctx, cwd, app.SearchFilters{Query: "time filter", UpdatedBefore: &between})
	if err != nil || len(before) != 1 || before[0].Entry.ID != old.Entry.ID {
		t.Fatalf("updated-before: %+v %v", before, err)
	}
	both, err := s.Search(ctx, cwd, app.SearchFilters{Query: "time filter", UpdatedAfter: &old.Entry.UpdatedAt, UpdatedBefore: &new.Entry.UpdatedAt})
	if err != nil || len(both) != 2 {
		t.Fatalf("inclusive window: %+v %v", both, err)
	}
}

func TestSearchCommitRangeFilters(t *testing.T) {
	ctx := context.Background()
	cwd := searchRepo(t)
	db, err := zova.Open(filepath.Join(t.TempDir(), "commitrange.zova"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := app.New(db)
	a, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Superseded choice", Body: "commit range"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Entry.StartCommit == nil || *a.Entry.StartCommit == "" {
		t.Fatal("missing start commit")
	}
	// Create a real commit so the supersede boundary is a distinct SHA.
	if err := os.WriteFile(filepath.Join(cwd, "marker.txt"), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", cwd, "add", ".").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	if out, err := exec.Command("git", "-C", cwd, "-c", "user.name=T", "-c", "user.email=t@e.com", "commit", "-qm", "boundary").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	b, err := s.Add(ctx, cwd, model.Decision, app.CreateInput{Title: "Current choice", Body: "commit range", Supersedes: a.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Re-fetch: supersede closes the stored entry's commit range, not the
	// snapshot returned by the original Add.
	a, err = s.Get(ctx, cwd, a.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	start := *a.Entry.StartCommit
	end := a.Entry.EndCommit
	if end == nil || *end == "" {
		t.Fatal("supersede did not close the superseded entry's commit range")
	}
	if b.Entry.StartCommit == nil || *b.Entry.StartCommit != *end {
		t.Fatalf("superseding entry should start at the boundary: %+v", b.Entry.StartCommit)
	}
	// commit-start pins the entry whose life began at that commit.
	byStart, err := s.Search(ctx, cwd, app.SearchFilters{CommitStart: start})
	if err != nil || len(byStart) != 1 || byStart[0].Entry.ID != a.Entry.ID {
		t.Fatalf("commit-start: %+v %v", byStart, err)
	}
	// commit-end pins entries closed at that commit.
	byEnd, err := s.Search(ctx, cwd, app.SearchFilters{CommitEnd: *end})
	if err != nil || len(byEnd) != 1 || byEnd[0].Entry.ID != a.Entry.ID {
		t.Fatalf("commit-end: %+v %v", byEnd, err)
	}
	// The bounds compose into a single-entry range query.
	ranged, err := s.Search(ctx, cwd, app.SearchFilters{CommitStart: start, CommitEnd: *end})
	if err != nil || len(ranged) != 1 || ranged[0].Entry.ID != a.Entry.ID {
		t.Fatalf("range composition: %+v %v", ranged, err)
	}
	// The legacy commit filter matches either bound: the closed entry via
	// end_commit and the superseding entry via start_commit.
	either, err := s.Search(ctx, cwd, app.SearchFilters{Commit: *end})
	if err != nil || len(either) != 2 {
		t.Fatalf("commit either-bound: %+v %v", either, err)
	}
	// Match metadata explains the commit hit.
	if m := byStart[0].Match; m == nil || !contains(m, "commit") {
		t.Fatalf("match metadata: %v", m)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
