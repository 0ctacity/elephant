package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
)

// MaxEvidencePerEntry bounds verification work per fact.
const MaxEvidencePerEntry = 50

// EvidenceView pairs stored evidence with its computed verification state.
type EvidenceView struct {
	model.Evidence
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// EvidenceReport summarizes verification for one entry.
type EvidenceReport struct {
	EntryID  string         `json:"entry_id"`
	Evidence []EvidenceView `json:"evidence"`
	Counts   map[string]int `json:"counts"`
}

func (r *EvidenceReport) add(view EvidenceView) {
	r.Evidence = append(r.Evidence, view)
	r.Counts[view.State]++
}

func newEvidenceReport(entryID string) EvidenceReport {
	return EvidenceReport{EntryID: entryID, Evidence: []EvidenceView{}, Counts: map[string]int{}}
}

// verifyEvidence compares recorded blob metadata with the current working
// tree. It never writes: the caller decides whether to refresh or retire.
func verifyEvidence(ctx context.Context, root string, evidence model.Evidence) (EvidenceView, error) {
	view := EvidenceView{Evidence: evidence}
	if root == "" {
		view.State = model.EvidenceUnavailable
		view.Detail = "repository metadata is unavailable, so the file cannot be compared"
		return view, nil
	}
	blob, err := gitrepo.FileBlob(ctx, root, evidence.Path)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return view, ctxErr
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		view.State = model.EvidenceMissing
		view.Detail = "the recorded file is not present in the working tree"
	case errors.Is(err, gitrepo.ErrNotRegularFile):
		view.State = model.EvidenceUnavailable
		view.Detail = "the recorded path is not a regular file"
	case err != nil:
		view.State = model.EvidenceUnavailable
		view.Detail = "the file or its Git blob could not be read"
	case evidence.Blob == "":
		view.State = model.EvidenceUnavailable
		view.Detail = "no blob was recorded for this evidence"
	case blob == evidence.Blob:
		view.State = model.EvidenceUnchanged
		view.Detail = "the file content still matches the recorded blob"
	default:
		view.State = model.EvidenceChanged
		view.Detail = "the file content differs from the recorded blob"
	}
	return view, nil
}

func blobForEvidence(ctx context.Context, g gitrepo.Metadata, path string) (string, error) {
	blob, err := gitrepo.FileBlob(ctx, g.Root, path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: evidence path %s does not exist in the working tree", model.ErrInvalidInput, path)
	case errors.Is(err, gitrepo.ErrNotRegularFile):
		return "", fmt.Errorf("%w: evidence path %s is not a regular file", model.ErrInvalidInput, path)
	}
	return blob, err
}
