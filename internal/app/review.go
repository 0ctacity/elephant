package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

// Finding codes are stable across human and JSON output.
const (
	CodeMissingFile         = "MISSING_FILE"
	CodeStaleTask           = "STALE_TASK"
	CodeUnverifiedFact      = "UNVERIFIED_FACT"
	CodeDanglingRelation    = "DANGLING_RELATION"
	CodeSupersessionAnomaly = "SUPERSESSION_ANOMALY"
	CodeUnknownActor        = "UNKNOWN_ACTOR"
	CodeStaleRemote         = "STALE_REMOTE"
	CodeEvidenceUnavailable = "EVIDENCE_UNAVAILABLE"
)

// ReviewFinding is one deterministic, read-only health candidate.
type ReviewFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
	EntryID  string `json:"entry_id,omitempty"`
	Detail   string `json:"detail"`
	Action   string `json:"action"`
}

// ReviewOptions bounds all thresholds (days, 1–3650).
type ReviewOptions struct {
	StaleTaskDays      int
	UnverifiedFactDays int
	StaleRemoteDays    int
}

func defaultReviewOptions() ReviewOptions {
	return ReviewOptions{StaleTaskDays: 30, UnverifiedFactDays: 90, StaleRemoteDays: 30}
}

func (o *ReviewOptions) withDefaults() ReviewOptions {
	out := defaultReviewOptions()
	if o == nil {
		return out
	}
	if o.StaleTaskDays > 0 {
		out.StaleTaskDays = o.StaleTaskDays
	}
	if o.UnverifiedFactDays > 0 {
		out.UnverifiedFactDays = o.UnverifiedFactDays
	}
	if o.StaleRemoteDays > 0 {
		out.StaleRemoteDays = o.StaleRemoteDays
	}
	return out
}

func (o ReviewOptions) validate() error {
	for _, v := range []int{o.StaleTaskDays, o.UnverifiedFactDays, o.StaleRemoteDays} {
		if v < 1 || v > 3650 {
			return fmt.Errorf("%w: review thresholds must be 1–3650 days", model.ErrInvalidInput)
		}
	}
	return nil
}

// Review scans local and received state without mutating anything.
func (s *Service) Review(ctx context.Context, cwd string, opts *ReviewOptions) (out []ReviewFinding, err error) {
	o := opts.withDefaults()
	if err = o.validate(); err != nil {
		return nil, err
	}
	out = []ReviewFinding{}
	now := time.Now().UTC()
	g, err := gitrepo.Inspect(ctx, cwd)
	if err != nil {
		return nil, err
	}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		local, err := resolve(tx, g)
		if err != nil {
			return err
		}
		type table struct {
			project model.Project
			source  string
		}
		tables := []table{{local, "local"}}
		remotes, err := tx.Remotes()
		if err != nil {
			return err
		}
		for _, r := range remotes {
			if r.ElephantID == "" {
				continue
			}
			src, err := tx.Source(g.Identity, r.ElephantID)
			if err != nil {
				continue
			}
			tables = append(tables, table{src, "remote:" + r.ElephantID})
		}
		for _, t := range tables {
			entries, err := allEntries(tx, t.project)
			if err != nil {
				return err
			}
			ids := map[string]model.Entry{}
			for _, e := range entries {
				ids[e.ID] = e
			}
			for _, e := range entries {
				if e.ActorID == "" || e.ActorID == "unknown" {
					out = append(out, ReviewFinding{Code: CodeUnknownActor, Severity: "low", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("%s %s has unknown actor", e.Kind, e.ID), Action: "recreate with ELEPHANT_ACTOR_ID set or update provenance"})
				}
				if e.Kind == model.Task && (e.Status == "open" || e.Status == "active" || e.Status == "blocked") {
					if now.Sub(e.UpdatedAt) > time.Duration(o.StaleTaskDays)*24*time.Hour {
						out = append(out, ReviewFinding{Code: CodeStaleTask, Severity: "low", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("task %s untouched for %d days (status %s)", e.ID, o.StaleTaskDays, e.Status), Action: "complete, cancel, or update the task explicitly"})
					}
				}
				if e.Kind == model.Fact && e.Status == "active" {
					if now.Sub(e.UpdatedAt) > time.Duration(o.UnverifiedFactDays)*24*time.Hour {
						out = append(out, ReviewFinding{Code: CodeUnverifiedFact, Severity: "low", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("fact %s not verified for %d days", e.ID, o.UnverifiedFactDays), Action: "verify the fact explicitly or retire it"})
					}
				}
				if e.Kind == model.Decision && e.Status == "superseded" {
					incoming, err := tx.Incoming(model.EntryNode(t.project, e.ID))
					if err != nil {
						// Storage without reverse edges: report unavailable, not inconsistency.
						out = append(out, ReviewFinding{Code: CodeSupersessionAnomaly, Severity: "medium", Source: t.source, EntryID: e.ID, Detail: "supersession graph unavailable for this entry", Action: "inspect the decision and its replacement explicitly"})
						continue
					}
					found := false
					for _, r := range incoming {
						if r.Type == "supersedes" {
							found = true
							break
						}
					}
					if !found {
						out = append(out, ReviewFinding{Code: CodeSupersessionAnomaly, Severity: "medium", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("decision %s is superseded but has no incoming supersedes edge", e.ID), Action: "inspect the decision and link its replacement explicitly"})
					}
				}
				links, err := tx.Relations(model.EntryNode(t.project, e.ID))
				if err != nil {
					return err
				}
				for _, r := range links {
					if strings.HasPrefix(r.To, "file:") {
						if t.source == "local" {
							file := strings.TrimPrefix(r.To, "file:"+t.project.ID+":")
							if _, statErr := os.Stat(filepath.Join(g.Root, file)); statErr != nil && os.IsNotExist(statErr) {
								out = append(out, ReviewFinding{Code: CodeMissingFile, Severity: "medium", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("referenced file %q no longer exists", file), Action: "update the entry's file links explicitly"})
							} else if statErr != nil {
								out = append(out, ReviewFinding{Code: CodeMissingFile, Severity: "low", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("referenced file %q unavailable: %v", file, statErr), Action: "retry review when the checkout is available"})
							}
						} else {
							// Remote file targets cannot be confirmed from this checkout.
							out = append(out, ReviewFinding{Code: CodeMissingFile, Severity: "low", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("remote file reference %q cannot be confirmed locally", strings.TrimPrefix(r.To, "file:")), Action: "inspect the source checkout before adopting"})
						}
						continue
					}
					target := r.To
					if i := strings.LastIndex(target, ":"); i >= 0 {
						target = target[i+1:]
					}
					if _, ok := ids[target]; !ok {
						out = append(out, ReviewFinding{Code: CodeDanglingRelation, Severity: "high", Source: t.source, EntryID: e.ID, Detail: fmt.Sprintf("relation %s points at unavailable entry %s", r.Type, target), Action: "remove the relation or adopt its target explicitly"})
					}
				}
			}
		}
		// Remote staleness uses peer registration freshness.
		for _, r := range remotes {
			if r.ElephantID == "" {
				out = append(out, ReviewFinding{Code: CodeStaleRemote, Severity: "low", Source: "local", Detail: fmt.Sprintf("remote %q has no established identity", r.Name), Action: "run remote ensure-project for " + r.Name})
				continue
			}
			if now.Sub(r.UpdatedAt) > time.Duration(o.StaleRemoteDays)*24*time.Hour {
				out = append(out, ReviewFinding{Code: CodeStaleRemote, Severity: "low", Source: "remote:" + r.ElephantID, Detail: fmt.Sprintf("remote %q not seen for %d days", r.Name, o.StaleRemoteDays), Action: "contact the peer or remove it explicitly"})
			}
		}
		return nil
	})
	if out == nil {
		out = []ReviewFinding{}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].EntryID < out[j].EntryID
	})
	return
}

func allEntries(tx storage.Tx, p model.Project) ([]model.Entry, error) {
	var out []model.Entry
	offset := 0
	for {
		page, err := tx.List(p, model.Filter{Limit: 200, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return out, nil
		}
		out = append(out, page...)
		offset += len(page)
		if len(page) < 200 {
			return out, nil
		}
	}
}
