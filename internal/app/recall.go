package app

import (
	"context"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

type RecallResult struct {
	Project          model.Project     `json:"project"`
	Git              gitrepo.Metadata  `json:"git"`
	Checkpoint       *CheckpointRecord `json:"latest_checkpoint,omitempty"`
	Tasks            []model.Entry     `json:"tasks"`
	Decisions        []model.Entry     `json:"decisions"`
	Facts            []model.Entry     `json:"facts"`
	Evidence         []EvidenceView    `json:"evidence"`
	RecentCompleted  []model.Entry     `json:"recent_completed_tasks"`
	RecentSuperseded []model.Entry     `json:"recent_superseded_decisions"`
	Relations        []model.Relation  `json:"relations"`
	Truncated        map[string]bool   `json:"truncated"`
}

// recallEvidenceLimit bounds verification work in one recall.
const recallEvidenceLimit = 50

func (s *Service) Recall(ctx context.Context, cwd, version string) (out RecallResult, err error) {
	return s.recallWith(ctx, version, func(fn func(storage.Tx, model.Project, gitrepo.Metadata) error) error { return s.within(ctx, cwd, fn) })
}
func (s *Service) recallWith(ctx context.Context, version string, within func(func(storage.Tx, model.Project, gitrepo.Metadata) error) error) (out RecallResult, err error) {
	out = RecallResult{Tasks: []model.Entry{}, Decisions: []model.Entry{}, Facts: []model.Entry{}, Evidence: []EvidenceView{}, RecentCompleted: []model.Entry{}, RecentSuperseded: []model.Entry{}, Relations: []model.Relation{}, Truncated: map[string]bool{}}
	err = within(func(tx storage.Tx, p model.Project, g gitrepo.Metadata) error {
		out.Project = p
		out.Git = g
		for _, group := range []struct {
			key    string
			kind   model.Kind
			status string
			limit  int
			dest   *[]model.Entry
		}{
			{"active_tasks", model.Task, "active", 50, &out.Tasks}, {"blocked_tasks", model.Task, "blocked", 50, &out.Tasks}, {"open_tasks", model.Task, "open", 50, &out.Tasks},
			{"decisions", model.Decision, "active", 50, &out.Decisions}, {"facts", model.Fact, "active", 50, &out.Facts},
			{"recent_completed_tasks", model.Task, "done", 10, &out.RecentCompleted}, {"recent_superseded_decisions", model.Decision, "superseded", 5, &out.RecentSuperseded},
		} {
			entries, err := tx.List(p, model.Filter{Kind: group.kind, Status: group.status, TargetVersion: version, Limit: group.limit + 1})
			if err != nil {
				return err
			}
			if len(entries) > group.limit {
				out.Truncated[group.key] = true
				entries = entries[:group.limit]
			}
			*group.dest = append(*group.dest, entries...)
			for _, e := range entries {
				links, err := tx.Relations(model.EntryNode(p, e.ID))
				if err != nil {
					return err
				}
				out.Relations = append(out.Relations, links...)
			}
		}
		for _, fact := range out.Facts {
			stored, err := tx.Evidence(p, fact.ID)
			if err != nil {
				return err
			}
			for _, evidence := range stored {
				if len(out.Evidence) >= recallEvidenceLimit {
					out.Truncated["evidence"] = true
					break
				}
				view, err := verifyEvidence(ctx, g.Root, evidence)
				if err != nil {
					return err
				}
				out.Evidence = append(out.Evidence, view)
			}
			if out.Truncated["evidence"] {
				break
			}
		}
		// Checkpoints are local-only: remote source recall must not leak
		// local session boundaries. Rows are additionally scoped by project
		// table in storage.
		if p.Scope == "local" {
			latest, err := tx.ListCheckpoints(p, 1, 0)
			if err != nil {
				return err
			}
			if len(latest) == 1 {
				links, err := tx.Relations(model.CheckpointNode(p.TableName, latest[0].ID))
				if err != nil {
					return err
				}
				out.Checkpoint = &CheckpointRecord{Checkpoint: latest[0], Relations: links}
			}
		}
		return nil
	})
	return
}
