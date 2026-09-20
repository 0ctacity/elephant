package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

// InboxGroup groups received additions by source Elephant and project without
// synthesizing canonical state across sources.
type InboxGroup struct {
	SourceElephantID string            `json:"source_elephant_id"`
	ProjectIdentity  string            `json:"project_identity"`
	ProjectName      string            `json:"project_name"`
	Entries          []model.Entry     `json:"entries"`
	Adopted          map[string]string `json:"adopted,omitempty"`
}

// DiffResult compares one remote source table with local state while keeping
// both boundaries explicit.
type DiffResult struct {
	Project    model.Project     `json:"project"`
	Source     model.Project     `json:"source"`
	Local      []model.Entry     `json:"local"`
	Remote     []model.Entry     `json:"remote"`
	Adopted    map[string]string `json:"adopted"`
	RemoteOnly []model.Entry     `json:"remote_only"`
}

// AdoptResult reports the new local entry plus its preserved provenance.
type AdoptResult struct {
	Entry            model.Entry      `json:"entry"`
	Relations        []model.Relation `json:"relations"`
	SourceElephantID string           `json:"source_elephant_id"`
	SourceEntryID    string           `json:"source_entry_id"`
	AlreadyAdopted   bool             `json:"already_adopted"`
}

func (s *Service) resolveSource(ctx context.Context, tx storage.Tx, selector string) (model.Project, error) {
	if validID(selector) {
		// Source UUID: find its table for the current project is resolved by caller;
		// here list all sources and match.
		sources, err := tx.Sources()
		if err != nil {
			return model.Project{}, err
		}
		for _, src := range sources {
			if src.SourceElephantID == selector {
				return src, nil
			}
		}
		return model.Project{}, fmt.Errorf("%w: unknown source %q", model.ErrInvalidInput, selector)
	}
	r, err := s.Remote(ctx, selector)
	if err != nil {
		return model.Project{}, err
	}
	if r.ElephantID == "" {
		return model.Project{}, fmt.Errorf("%w: remote identity unknown; ensure-project first or use source UUID", model.ErrInvalidInput)
	}
	sources, err := tx.Sources()
	if err != nil {
		return model.Project{}, err
	}
	for _, src := range sources {
		if src.SourceElephantID == r.ElephantID {
			return src, nil
		}
	}
	return model.Project{}, fmt.Errorf("%w: no received state from remote %q", model.ErrInvalidInput, selector)
}

func (s *Service) resolveSourceForProject(tx storage.Tx, identity, selector string, remotes []model.Remote) (model.Project, error) {
	if validID(selector) {
		return tx.Source(identity, selector)
	}
	for _, r := range remotes {
		if r.Name == selector {
			if r.ElephantID == "" {
				return model.Project{}, fmt.Errorf("%w: remote identity unknown; ensure-project first or use source UUID", model.ErrInvalidInput)
			}
			return tx.Source(identity, r.ElephantID)
		}
	}
	return model.Project{}, fmt.Errorf("%w: unknown remote %q", model.ErrInvalidInput, selector)
}

// Inbox lists received additions grouped by source Elephant and project.
func (s *Service) Inbox(ctx context.Context) (out []InboxGroup, err error) {
	out = []InboxGroup{}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		sources, err := tx.Sources()
		if err != nil {
			return err
		}
		for _, src := range sources {
			entries, err := tx.List(src, model.Filter{Limit: 200})
			if err != nil {
				return err
			}
			adoptedRows, err := tx.AdoptionsBySource(src.Identity, src.SourceElephantID)
			if err != nil {
				return err
			}
			adopted := map[string]string{}
			for _, a := range adoptedRows {
				adopted[a.SourceEntryID] = a.LocalEntryID
			}
			out = append(out, InboxGroup{SourceElephantID: src.SourceElephantID, ProjectIdentity: src.Identity, ProjectName: src.Name, Entries: entries, Adopted: adopted})
		}
		return nil
	})
	return
}

// Diff compares a remote source table with the local project state.
func (s *Service) Diff(ctx context.Context, cwd, selector string) (out DiffResult, err error) {
	g, err := gitrepo.Inspect(ctx, cwd)
	if err != nil {
		return out, err
	}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		local, err := resolve(tx, g)
		if err != nil {
			return err
		}
		remotes, err := tx.Remotes()
		if err != nil {
			return err
		}
		src, err := s.resolveSourceForProject(tx, g.Identity, selector, remotes)
		if err != nil {
			return err
		}
		out.Project = local
		out.Source = src
		out.Local = []model.Entry{}
		out.Remote = []model.Entry{}
		out.Adopted = map[string]string{}
		if out.Local, err = tx.List(local, model.Filter{Limit: 200}); err != nil {
			return err
		}
		if out.Remote, err = tx.List(src, model.Filter{Limit: 200}); err != nil {
			return err
		}
		adoptedRows, err := tx.AdoptionsBySource(g.Identity, src.SourceElephantID)
		if err != nil {
			return err
		}
		for _, a := range adoptedRows {
			out.Adopted[a.SourceEntryID] = a.LocalEntryID
		}
		for _, e := range out.Remote {
			if _, ok := out.Adopted[e.ID]; !ok {
				out.RemoteOnly = append(out.RemoteOnly, e)
			}
		}
		if out.RemoteOnly == nil {
			out.RemoteOnly = []model.Entry{}
		}
		return nil
	})
	return
}

// Adopt creates a new local entry from a received remote entry, preserving
// provenance and remaining idempotent on repeat.
func (s *Service) Adopt(ctx context.Context, cwd, entryID, selector string) (out AdoptResult, err error) {
	g, err := gitrepo.Inspect(ctx, cwd)
	if err != nil {
		return out, err
	}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		local, err := resolve(tx, g)
		if err != nil {
			return err
		}
		// Resolve inside the same transaction to keep boundaries consistent.
		remotes, err := tx.Remotes()
		if err != nil {
			return err
		}
		src, err := s.resolveSourceForProject(tx, g.Identity, selector, remotes)
		if err != nil {
			return err
		}
		if existing, err := tx.Adoption(g.Identity, src.SourceElephantID, entryID); err == nil {
			e, err := tx.Get(local, existing.LocalEntryID)
			if err != nil {
				return err
			}
			links, err := tx.Relations(model.EntryNode(local, e.ID))
			if err != nil {
				return err
			}
			out = AdoptResult{Entry: e, Relations: links, SourceElephantID: src.SourceElephantID, SourceEntryID: entryID, AlreadyAdopted: true}
			return nil
		}
		remote, err := tx.Get(src, entryID)
		if err != nil {
			return err
		}
		remoteLinks, err := tx.Relations(model.EntryNode(src, remote.ID))
		if err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		e := model.Entry{ID: id.String(), ActorID: currentActor(), Kind: remote.Kind, Title: remote.Title, Body: remote.Body, Status: model.DefaultStatus(remote.Kind), TargetVersion: remote.TargetVersion, StartCommit: optional(g.Head), CreatedAt: now, UpdatedAt: now}
		if err = e.Validate(); err != nil {
			return err
		}
		if err = tx.Put(local, e); err != nil {
			return err
		}
		// Copy file links only when the path is still valid; copy entry
		// relations only when the local target can be resolved safely.
		adoptedBySource := map[string]string{}
		prior, err := tx.AdoptionsBySource(g.Identity, src.SourceElephantID)
		if err != nil {
			return err
		}
		for _, a := range prior {
			adoptedBySource[a.SourceEntryID] = a.LocalEntryID
		}
		for _, r := range remoteLinks {
			if strings.HasPrefix(r.To, "file:"+src.ID+":") {
				file := strings.TrimPrefix(r.To, "file:"+src.ID+":")
				clean, err := model.CleanFile(file)
				if err != nil {
					continue
				}
				_ = tx.Link(local, model.Relation{From: model.EntryNode(local, e.ID), Type: model.FileEdge(e.Kind), To: "file:" + local.ID + ":" + clean})
				continue
			}
			// Entry edge: remote node IDs are entry:<id> for local-scope
			// sources in tests (Source with empty scope). Resolve the raw
			// entry ID suffix and map through prior adoptions.
			target := r.To
			if i := strings.LastIndex(target, ":"); i >= 0 {
				target = target[i+1:]
			}
			localTarget, ok := adoptedBySource[target]
			if !ok {
				// Only reuse targets that already exist locally with a
				// compatible kind; otherwise skip rather than fail.
				other, err := tx.Get(local, target)
				if err != nil {
					continue
				}
				if err = model.ValidateRelation(e.Kind, r.Type, other.Kind); err != nil {
					continue
				}
				localTarget = other.ID
			} else {
				other, err := tx.Get(local, localTarget)
				if err != nil {
					continue
				}
				if err = model.ValidateRelation(e.Kind, r.Type, other.Kind); err != nil {
					continue
				}
			}
			_ = tx.Link(local, model.Relation{From: model.EntryNode(local, e.ID), Type: r.Type, To: model.EntryNode(local, localTarget)})
		}
		links, err := tx.Relations(model.EntryNode(local, e.ID))
		if err != nil {
			return err
		}
		adoptID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if err = tx.RecordAdoption(model.Adoption{ID: adoptID.String(), ProjectIdentity: g.Identity, SourceElephantID: src.SourceElephantID, SourceEntryID: remote.ID, LocalEntryID: e.ID, Kind: string(e.Kind), CreatedAt: now}); err != nil {
			return err
		}
		out = AdoptResult{Entry: e, Relations: links, SourceElephantID: src.SourceElephantID, SourceEntryID: remote.ID}
		return nil
	})
	return
}
