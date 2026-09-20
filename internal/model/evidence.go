package model

import (
	"fmt"
	"strings"
	"time"
)

// Evidence records where a fact's knowledge came from. Blob holds the stable
// hex digest of the selected content (one line without its terminator, or
// the whole file when Line is 0) as read from Commit. Elephant compares that
// digest with the recorded commit and then the working tree; it never judges
// whether a changed fact is still true.
type Evidence struct {
	ID         string    `json:"id"`
	EntryID    string    `json:"entry_id"`
	Path       string    `json:"path"`
	Line       int       `json:"line,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Blob       string    `json:"blob,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Evidence states are computed during verification and are never stored.
const (
	EvidenceUnchanged   = "unchanged"
	EvidenceChanged     = "changed"
	EvidenceMissing     = "missing"
	EvidenceUnavailable = "unavailable"
)

func ValidEvidenceState(s string) bool {
	switch s {
	case EvidenceUnchanged, EvidenceChanged, EvidenceMissing, EvidenceUnavailable:
		return true
	}
	return false
}

func (e Evidence) Validate() error {
	if e.ID == "" || e.EntryID == "" || e.CreatedAt.IsZero() || e.VerifiedAt.Before(e.CreatedAt) {
		return fmt.Errorf("%w: evidence ID, entry ID, and timestamps are required", ErrInvalidInput)
	}
	if _, err := CleanFile(e.Path); err != nil {
		return err
	}
	if e.Line < 0 || e.Line > 1_000_000 {
		return fmt.Errorf("%w: evidence line must be between 1 and 1000000", ErrInvalidInput)
	}
	for _, value := range []struct {
		name  string
		value string
		limit int
	}{{"commit", e.Commit, 128}, {"blob", e.Blob, 128}} {
		if len(value.value) > value.limit || strings.ContainsAny(value.value, " \t\x00") {
			return fmt.Errorf("%w: evidence %s must be at most %d characters without whitespace", ErrInvalidInput, value.name, value.limit)
		}
	}
	return nil
}

// CleanEvidenceLine rejects negative lines the same way CleanFile rejects
// absolute paths, so CLI and machine inputs stay consistent.
func CleanEvidenceLine(line int) (int, error) {
	if line < 0 || line > 1_000_000 {
		return 0, fmt.Errorf("%w: evidence line must be between 1 and 1000000", ErrInvalidInput)
	}
	return line, nil
}
