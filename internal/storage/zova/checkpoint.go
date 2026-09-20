package zova

import (
	"fmt"

	native "github.com/ata-sesli/zova/bindings/go"

	"elephant/internal/model"
)

const checkpointColumns = "id,project_identity,summary,completed,next,commands,failures,actor_id,start_commit,end_commit,created_at,updated_at"

func (t *transaction) ensureCheckpointsTable() error {
	err := t.db.Exec(`CREATE TABLE IF NOT EXISTS checkpoints(id TEXT PRIMARY KEY,project_identity TEXT NOT NULL,summary TEXT NOT NULL,completed TEXT NOT NULL,next TEXT NOT NULL,commands TEXT NOT NULL,failures TEXT NOT NULL,actor_id TEXT NOT NULL,start_commit TEXT,end_commit TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS checkpoints_project ON checkpoints(project_identity,created_at DESC,id DESC);`)
	if err != nil {
		return fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	return nil
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
	return model.Checkpoint{
		ID: value(r[0]), Summary: value(r[2]), Completed: value(r[3]), Next: value(r[4]),
		Commands: value(r[5]), Failures: value(r[6]), ActorID: value(r[7]),
		StartCommit: r[8], EndCommit: r[9], CreatedAt: created, UpdatedAt: updated,
	}, nil
}

func (t *transaction) PutCheckpoint(p model.Project, c model.Checkpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := t.ensureCheckpointsTable(); err != nil {
		return err
	}
	_, err := t.query("INSERT INTO checkpoints("+checkpointColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET summary=excluded.summary,completed=excluded.completed,next=excluded.next,commands=excluded.commands,failures=excluded.failures,actor_id=excluded.actor_id,start_commit=excluded.start_commit,end_commit=excluded.end_commit,updated_at=excluded.updated_at",
		c.ID, p.Identity, c.Summary, c.Completed, c.Next, c.Commands, c.Failures, c.ActorID, c.StartCommit, c.EndCommit, stamp(c.CreatedAt), stamp(c.UpdatedAt))
	if err != nil {
		return err
	}
	return t.db.PutGraphNode(native.GraphNodeInput{GraphName: graph, NodeID: model.CheckpointNode(c.ID), Kind: "checkpoint", TargetType: native.GraphTargetExternal, TargetNamespace: &p.Identity, TargetRef: &c.ID})
}

func (t *transaction) GetCheckpoint(p model.Project, id string) (model.Checkpoint, error) {
	if err := t.ensureCheckpointsTable(); err != nil {
		return model.Checkpoint{}, err
	}
	rows, err := t.query("SELECT "+checkpointColumns+" FROM checkpoints WHERE id=? AND project_identity=?", id, p.Identity)
	if err != nil {
		return model.Checkpoint{}, err
	}
	if len(rows) == 0 {
		return model.Checkpoint{}, model.ErrEntryNotFound
	}
	return decodeCheckpoint(rows[0])
}

func (t *transaction) ListCheckpoints(p model.Project, limit, offset int) ([]model.Checkpoint, error) {
	if err := t.ensureCheckpointsTable(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	if offset < 0 {
		return nil, model.ErrInvalidInput
	}
	rows, err := t.query("SELECT "+checkpointColumns+" FROM checkpoints WHERE project_identity=? ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", p.Identity, limit, offset)
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
