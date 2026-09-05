package app

import (
	"context"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

type Orientation struct {
	Project          model.Project    `json:"project"`
	Git              gitrepo.Metadata `json:"git"`
	Tasks            []model.Entry    `json:"tasks"`
	Decisions        []model.Entry    `json:"decisions"`
	Facts            []model.Entry    `json:"facts"`
	RecentCompleted  []model.Entry    `json:"recent_completed_tasks"`
	RecentSuperseded []model.Entry    `json:"recent_superseded_decisions"`
	Relations        []model.Relation `json:"relations"`
	Truncated        map[string]bool  `json:"truncated"`
}

func (s *Service) Orient(ctx context.Context, cwd, version string) (out Orientation, err error) {
	out = Orientation{Tasks: []model.Entry{}, Decisions: []model.Entry{}, Facts: []model.Entry{}, RecentCompleted: []model.Entry{}, RecentSuperseded: []model.Entry{}, Relations: []model.Relation{}, Truncated: map[string]bool{}}
	err = s.within(ctx, cwd, func(tx storage.Tx, p model.Project, g gitrepo.Metadata) error {
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
				links, err := tx.Relations("entry:" + e.ID)
				if err != nil {
					return err
				}
				out.Relations = append(out.Relations, links...)
			}
		}
		return nil
	})
	return
}
