package model

import (
	"fmt"
	"strings"
	"time"
)

// Checkpoint list bounds keep session boundaries concise and bounded.
const (
	MaxCheckpointItems    = 50
	MaxCheckpointItemSize = 2000
)

// Checkpoint marks a concise boundary between coding sessions: what changed,
// what was tried, and where the next agent should resume. Completed work,
// next actions, commands, and failures are ordered lists.
type Checkpoint struct {
	ID          string    `json:"id"`
	ActorID     string    `json:"actor_id"`
	Summary     string    `json:"summary"`
	Completed   []string  `json:"completed,omitempty"`
	Next        []string  `json:"next,omitempty"`
	Commands    []string  `json:"commands,omitempty"`
	Failures    []string  `json:"failures,omitempty"`
	StartCommit *string   `json:"start_commit"`
	EndCommit   *string   `json:"end_commit"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (c Checkpoint) Validate() error {
	if strings.TrimSpace(c.Summary) == "" || len(c.Summary) > 2000 || strings.ContainsRune(c.Summary, 0) {
		return fmt.Errorf("%w: summary (1-2000 bytes) required", ErrInvalidInput)
	}
	for _, v := range []struct {
		name   string
		values []string
	}{
		{"completed", c.Completed}, {"next", c.Next}, {"commands", c.Commands}, {"failures", c.Failures},
	} {
		if len(v.values) > MaxCheckpointItems {
			return fmt.Errorf("%w: checkpoint %s accepts at most %d items", ErrInvalidInput, v.name, MaxCheckpointItems)
		}
		for _, item := range v.values {
			if strings.TrimSpace(item) == "" || len(item) > MaxCheckpointItemSize || strings.ContainsRune(item, 0) {
				return fmt.Errorf("%w: checkpoint %s items must be 1-%d bytes without NUL", ErrInvalidInput, v.name, MaxCheckpointItemSize)
			}
		}
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.Before(c.CreatedAt) {
		return fmt.Errorf("%w: checkpoint timestamps required", ErrInvalidInput)
	}
	for _, commit := range []*string{c.StartCommit, c.EndCommit} {
		if commit != nil && (len(*commit) > 128 || strings.ContainsAny(*commit, " \t\x00") || strings.TrimSpace(*commit) == "") {
			return fmt.Errorf("%w: checkpoint commit must be 1-128 non-whitespace characters", ErrInvalidInput)
		}
	}
	return nil
}

// CheckpointNode scopes a checkpoint in the shared graph by owning project
// table, so identical checkpoint IDs in different source tables never share
// a node. The distinct prefix keeps checkpoints clear of entry: and file:.
func CheckpointNode(table, id string) string { return "checkpoint:" + table + ":" + id }
