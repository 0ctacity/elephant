package zova

import (
	"encoding/json"
	"fmt"

	native "github.com/ata-sesli/zova/bindings/go"

	"elephant/internal/model"
)

const checkpointColumns = "id,project_table,summary,completed,next,commands,failures,actor_id,start_commit,end_commit,created_at,updated_at"

// migrateCheckpoints upgrades schema 3 with the table-scoped checkpoints
// table. Pre-release identity-scoped rows are mapped onto their local project
// table and their text fields wrapped as single-item lists; rows without a
// local project are skipped rather than failing the migration. It is
// idempotent and transactional with the version stamp.
func (t *transaction) migrateCheckpoints() error {
	rows, err := t.query("SELECT name FROM sqlite_master WHERE type='table' AND name='checkpoints'")
	if err != nil {
		return err
	}
	if len(rows) == 1 {
		columns, err := t.query("PRAGMA table_info(checkpoints)")
		if err != nil {
			return err
		}
		legacy := false
		for _, column := range columns {
			if len(column) > 1 && value(column[1]) == "project_identity" {
				legacy = true
				break
			}
		}
		if legacy {
			if err = t.migrateLegacyCheckpoints(); err != nil {
				return err
			}
		}
	}
	err = t.db.Exec(`CREATE TABLE IF NOT EXISTS checkpoints(id TEXT PRIMARY KEY,project_table TEXT NOT NULL,summary TEXT NOT NULL,completed TEXT NOT NULL,next TEXT NOT NULL,commands TEXT NOT NULL,failures TEXT NOT NULL,actor_id TEXT NOT NULL,start_commit TEXT,end_commit TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS checkpoints_project ON checkpoints(project_table,created_at DESC,id DESC);`)
	if err != nil {
		return fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	_, err = t.query("UPDATE elephant_meta SET value='4' WHERE key='schema_version'")
	return err
}

// migrateLegacyCheckpoints moves pre-release rows keyed by repository
// identity onto their local project table, then replaces the old table with
// the current shape. Rows without a local project are skipped rather than
// failing the migration.
func (t *transaction) migrateLegacyCheckpoints() error {
	rows, err := t.query("SELECT id,project_identity,summary,completed,next,commands,failures,actor_id,start_commit,end_commit,created_at,updated_at FROM checkpoints")
	if err != nil {
		return err
	}
	wrap := func(text string) string {
		if text == "" {
			return "[]"
		}
		encoded, err := json.Marshal([]string{text})
		if err != nil {
			return "[]"
		}
		return string(encoded)
	}
	var inserts [][]any
	for _, row := range rows {
		projects, err := t.query("SELECT table_name FROM projects WHERE identity=?", value(row[1]))
		if err != nil {
			return err
		}
		if len(projects) != 1 {
			continue
		}
		inserts = append(inserts, []any{
			value(row[0]), value(projects[0][0]), value(row[2]),
			wrap(value(row[3])), wrap(value(row[4])), wrap(value(row[5])), wrap(value(row[6])),
			value(row[7]), row[8], row[9], value(row[10]), value(row[11]),
		})
	}
	if err = t.db.Exec(`DROP TABLE checkpoints;`); err != nil {
		return fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	if err = t.db.Exec(`CREATE TABLE checkpoints(id TEXT PRIMARY KEY,project_table TEXT NOT NULL,summary TEXT NOT NULL,completed TEXT NOT NULL,next TEXT NOT NULL,commands TEXT NOT NULL,failures TEXT NOT NULL,actor_id TEXT NOT NULL,start_commit TEXT,end_commit TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);`); err != nil {
		return fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	for _, values := range inserts {
		if _, err = t.query("INSERT INTO checkpoints("+checkpointColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", values...); err != nil {
			return err
		}
	}
	return nil
}

func encodeCheckpointList(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("%w: encode checkpoint list: %v", model.ErrInvalidInput, err)
	}
	return string(encoded), nil
}

func decodeCheckpointList(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("%w: invalid checkpoint list %q", model.ErrStorage, raw)
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func decodeCheckpoint(r []*string) (model.Checkpoint, error) {
	created, err := parseTime(r[10])
	if err != nil {
		return model.Checkpoint{}, err
	}
	updated, err := parseTime(r[11])
	if err != nil {
		return model.Checkpoint{}, err
	}
	completed, err := decodeCheckpointList(value(r[3]))
	if err != nil {
		return model.Checkpoint{}, err
	}
	next, err := decodeCheckpointList(value(r[4]))
	if err != nil {
		return model.Checkpoint{}, err
	}
	commands, err := decodeCheckpointList(value(r[5]))
	if err != nil {
		return model.Checkpoint{}, err
	}
	failures, err := decodeCheckpointList(value(r[6]))
	if err != nil {
		return model.Checkpoint{}, err
	}
	return model.Checkpoint{
		ID: value(r[0]), Summary: value(r[2]), Completed: completed, Next: next,
		Commands: commands, Failures: failures, ActorID: value(r[7]),
		StartCommit: r[8], EndCommit: r[9], CreatedAt: created, UpdatedAt: updated,
	}, nil
}

// PutCheckpoint stores one checkpoint row scoped to the owning project table.
// The checkpoints table is created by the schema v4 migration; reads and
// writes never create schema objects.
func (t *transaction) PutCheckpoint(p model.Project, c model.Checkpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	name, err := table(p)
	if err != nil {
		return err
	}
	completed, err := encodeCheckpointList(c.Completed)
	if err != nil {
		return err
	}
	next, err := encodeCheckpointList(c.Next)
	if err != nil {
		return err
	}
	commands, err := encodeCheckpointList(c.Commands)
	if err != nil {
		return err
	}
	failures, err := encodeCheckpointList(c.Failures)
	if err != nil {
		return err
	}
	_, err = t.query("INSERT INTO checkpoints("+checkpointColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET summary=excluded.summary,completed=excluded.completed,next=excluded.next,commands=excluded.commands,failures=excluded.failures,actor_id=excluded.actor_id,start_commit=excluded.start_commit,end_commit=excluded.end_commit,updated_at=excluded.updated_at",
		c.ID, name, c.Summary, completed, next, commands, failures, c.ActorID, c.StartCommit, c.EndCommit, stamp(c.CreatedAt), stamp(c.UpdatedAt))
	if err != nil {
		return err
	}
	namespace := name
	ref := c.ID
	return t.db.PutGraphNode(native.GraphNodeInput{GraphName: graph, NodeID: model.CheckpointNode(name, c.ID), Kind: "checkpoint", TargetType: native.GraphTargetExternal, TargetNamespace: &namespace, TargetRef: &ref})
}

func (t *transaction) GetCheckpoint(p model.Project, id string) (model.Checkpoint, error) {
	name, err := table(p)
	if err != nil {
		return model.Checkpoint{}, err
	}
	rows, err := t.query("SELECT "+checkpointColumns+" FROM checkpoints WHERE id=? AND project_table=?", id, name)
	if err != nil {
		return model.Checkpoint{}, err
	}
	if len(rows) == 0 {
		return model.Checkpoint{}, model.ErrEntryNotFound
	}
	return decodeCheckpoint(rows[0])
}

func (t *transaction) ListCheckpoints(p model.Project, limit, offset int) ([]model.Checkpoint, error) {
	name, err := table(p)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	if offset < 0 {
		return nil, model.ErrInvalidInput
	}
	rows, err := t.query("SELECT "+checkpointColumns+" FROM checkpoints WHERE project_table=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", name, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]model.Checkpoint, 0, len(rows))
	for _, r := range rows {
		c, err := decodeCheckpoint(r)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
