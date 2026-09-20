package model

import "time"

// SearchQuery filters deterministic text retrieval. Empty Query matches all
// entries when other filters are set; results sort newest first with ID
// tie-break and are bounded by Limit. CommitStart and CommitEnd match the
// inclusive start and end bounds of an entry's commit range (start_commit
// and end_commit), matching issue #7's commit-range filter rather than
// exact equality on a single commit.
type SearchQuery struct {
	Query         string
	Kind          Kind
	Status        string
	Actor         string
	TargetVersion string
	Commit        string
	CommitStart   string
	CommitEnd     string
	File          string
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	Limit         int
	Offset        int
}
