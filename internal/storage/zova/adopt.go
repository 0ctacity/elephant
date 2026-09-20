package zova

import (
	"fmt"

	"elephant/internal/model"
)

func (t *transaction) ensureAdoptionsTable() error {
	err := t.db.Exec(`CREATE TABLE IF NOT EXISTS adoptions(id TEXT PRIMARY KEY,project_identity TEXT NOT NULL,source_elephant_id TEXT NOT NULL,source_entry_id TEXT NOT NULL,local_entry_id TEXT NOT NULL,kind TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(project_identity,source_elephant_id,source_entry_id));
CREATE INDEX IF NOT EXISTS adoptions_source ON adoptions(project_identity,source_elephant_id);`)
	if err != nil {
		return fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	return nil
}

// Sources lists every received remote source table without synthesizing
// canonical state across sources.
func (t *transaction) Sources() ([]model.Project, error) {
	rows, err := t.query("SELECT id,project_identity,project_name,table_name,scope,source_elephant_id,created_at,updated_at FROM project_tables WHERE scope='remote' ORDER BY project_identity,source_elephant_id")
	if err != nil {
		return nil, err
	}
	out := make([]model.Project, 0, len(rows))
	for _, r := range rows {
		c, err := parseTime(r[6])
		if err != nil {
			return nil, err
		}
		u, err := parseTime(r[7])
		if err != nil {
			return nil, err
		}
		out = append(out, model.Project{ID: value(r[0]), Identity: value(r[1]), Name: value(r[2]), TableName: value(r[3]), Scope: value(r[4]), SourceElephantID: value(r[5]), CreatedAt: c, UpdatedAt: u})
	}
	return out, nil
}

func (t *transaction) RecordAdoption(a model.Adoption) error {
	if err := t.ensureAdoptionsTable(); err != nil {
		return err
	}
	_, err := t.query("INSERT INTO adoptions VALUES(?,?,?,?,?,?,?) ON CONFLICT(project_identity,source_elephant_id,source_entry_id) DO NOTHING",
		a.ID, a.ProjectIdentity, a.SourceElephantID, a.SourceEntryID, a.LocalEntryID, a.Kind, stamp(a.CreatedAt))
	return err
}

func (t *transaction) Adoption(identity, source, entryID string) (model.Adoption, error) {
	if err := t.ensureAdoptionsTable(); err != nil {
		return model.Adoption{}, err
	}
	rows, err := t.query("SELECT id,project_identity,source_elephant_id,source_entry_id,local_entry_id,kind,created_at FROM adoptions WHERE project_identity=? AND source_elephant_id=? AND source_entry_id=?",
		identity, source, entryID)
	if err != nil {
		return model.Adoption{}, err
	}
	if len(rows) == 0 {
		return model.Adoption{}, model.ErrEntryNotFound
	}
	c, err := parseTime(rows[0][6])
	if err != nil {
		return model.Adoption{}, err
	}
	r := rows[0]
	return model.Adoption{ID: value(r[0]), ProjectIdentity: value(r[1]), SourceElephantID: value(r[2]), SourceEntryID: value(r[3]), LocalEntryID: value(r[4]), Kind: value(r[5]), CreatedAt: c}, nil
}

func (t *transaction) AdoptionsBySource(identity, source string) ([]model.Adoption, error) {
	if err := t.ensureAdoptionsTable(); err != nil {
		return nil, err
	}
	rows, err := t.query("SELECT id,project_identity,source_elephant_id,source_entry_id,local_entry_id,kind,created_at FROM adoptions WHERE project_identity=? AND source_elephant_id=? ORDER BY created_at,id",
		identity, source)
	if err != nil {
		return nil, err
	}
	out := make([]model.Adoption, 0, len(rows))
	for _, r := range rows {
		c, err := parseTime(r[6])
		if err != nil {
			return nil, err
		}
		out = append(out, model.Adoption{ID: value(r[0]), ProjectIdentity: value(r[1]), SourceElephantID: value(r[2]), SourceEntryID: value(r[3]), LocalEntryID: value(r[4]), Kind: value(r[5]), CreatedAt: c})
	}
	return out, nil
}
