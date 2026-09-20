package app

import (
	"context"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

// SourceInspection reports the auditable contents of one source table — local
// or remote — without modifying it. It is the read-only view used to verify
// imported archives and received remotes.
type SourceInspection struct {
	Source        model.Project      `json:"source"`
	Entries       []model.Entry      `json:"entries"`
	Evidence      []model.Evidence   `json:"evidence"`
	Checkpoints   []model.Checkpoint `json:"checkpoints"`
	AdoptionCount int                `json:"adoption_count"`
}

// InspectSource lists every record of one source table for the current
// project. It performs no mutations and preserves source separation: only the
// named source's rows are read.
func (s *Service) InspectSource(ctx context.Context, cwd, selector string) (out SourceInspection, err error) {
	g, err := gitrepo.Inspect(ctx, cwd)
	if err != nil {
		return out, err
	}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		// Accept a raw source ID (for example an imported archive) as well as
		// registered remote names.
		var src model.Project
		byID, idErr := tx.Source(g.Identity, selector)
		switch {
		case idErr == nil:
			src = byID
		default:
			remotes, err := tx.Remotes()
			if err != nil {
				return err
			}
			src, err = s.resolveSourceForProject(tx, g.Identity, selector, remotes)
			if err != nil {
				return err
			}
		}
		out.Source = src
		println("DEBUG inspect table:", src.TableName, "selector:", selector)
		out.Entries = []model.Entry{}
		out.Evidence = []model.Evidence{}
		out.Checkpoints = []model.Checkpoint{}
		offset := 0
		for {
			page, err := tx.List(src, model.Filter{Limit: 200, Offset: offset})
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			out.Entries = append(out.Entries, page...)
			offset += len(page)
			if len(page) < 200 {
				break
			}
		}
		for _, e := range out.Entries {
			rows, err := tx.Evidence(src, e.ID)
			if err != nil {
				return err
			}
			out.Evidence = append(out.Evidence, rows...)
		}
		for offset := 0; ; offset += 200 {
			page, err := tx.ListCheckpoints(src, 200, offset)
			println("DEBUG ListCheckpoints page:", len(page), "err:", err == nil, "table:", src.TableName)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			out.Checkpoints = append(out.Checkpoints, page...)
			if len(page) < 200 {
				break
			}
		}
		receipts, err := tx.AdoptionsBySource(g.Identity, src.SourceElephantID)
		if err != nil {
			return err
		}
		out.AdoptionCount = len(receipts)
		return nil
	})
	return
}
