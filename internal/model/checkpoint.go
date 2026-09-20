package model

import (
	"fmt"
	"strings"
	"time"
)

// Checkpoint marks a concise boundary between coding sessions: what changed,
// what was tried, and where the next agent should resume.
type Checkpoint struct {
	ID          string    `json:"id"`
	ActorID     string    `json:"actor_id"`
	Summary     string    `json:"summary"`
	Completed   string    `json:"completed,omitempty"`
	Next        string    `json:"next,omitempty"`
	Commands    string    `json:"commands,omitempty"`
	Failures    string    `json:"failures,omitempty"`
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
		name  string
		value string
	}{
		{"completed", c.Completed}, {"next", c.Next}, {"commands", c.Commands}, {"failures", c.Failures},
	} {
		if len(v.value) > 32768 || strings.ContainsRune(v.value, 0) {
			return fmt.Errorf("%w: checkpoint %s must be at most 32768 bytes", ErrInvalidInput, v.name)
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

// CheckpointNode scopes a checkpoint in the shared graph. Checkpoints use a
// distinct prefix so they never collide with entry: or file: nodes.
func CheckpointNode(id string) string { return "checkpoint:" + id }
