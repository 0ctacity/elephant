package model

import "time"

// SearchQuery filters deterministic text retrieval. Empty Query matches all
// entries when other filters are set; results sort newest first with ID
// tie-break and are bounded by Limit. Commit-range and file filters are not
// part of the store contract: Git ancestry and graph relations live outside
// SQL, so the application layer applies them before pagination.
type SearchQuery struct {
	Query         string
	Kind          Kind
	Status        string
	Actor         string
	TargetVersion string
	File          string
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	Limit         int
	Offset        int
}
