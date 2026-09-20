package model

import "time"

// Adoption records an explicit promotion of one remote entry into local truth.
// The adopted local row receives a new local ID; this receipt preserves the
// source provenance (source Elephant, original entry ID) for idempotency.
type Adoption struct {
	ID               string    `json:"id"`
	ProjectIdentity  string    `json:"project_identity"`
	SourceElephantID string    `json:"source_elephant_id"`
	SourceEntryID    string    `json:"source_entry_id"`
	LocalEntryID     string    `json:"local_entry_id"`
	Kind             string    `json:"kind"`
	CreatedAt        time.Time `json:"created_at"`
}
