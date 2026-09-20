package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"

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

// digestEvidenceContent is the stable digest recorded for evidence: hex
// SHA-256 over the selected bytes (one line without its terminator, or the
// whole file when no line is pinned).
func digestEvidenceContent(selected []byte) string {
	sum := sha256.Sum256(selected)
	return fmt.Sprintf("%x", sum)
}

// selectEvidenceLine returns the bytes covered by a pinned line. Line 0
// selects the whole file; otherwise lines are one-based, terminators are
// excluded, and a trailing carriage return is stripped. An out-of-range line
// is an error carrying the file's line count.
func selectEvidenceLine(content []byte, line int) ([]byte, error) {
	if line == 0 {
		return content, nil
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	if line < 1 || line > len(lines) {
		return nil, fmt.Errorf("line %d out of range: file has %d lines", line, len(lines))
	}
	return []byte(lines[line-1]), nil
}

// verifyEvidence compares the recorded commit digest with the current working
// tree in two separate steps. It never writes: the caller decides whether to
// refresh or retire. The recorded commit is checked first: when it or its
// object cannot be read, the baseline is unverifiable. Only then is the
// current checkout compared, yielding missing, changed, or unchanged.
func verifyEvidence(ctx context.Context, root string, evidence model.Evidence) (EvidenceView, error) {
	view := EvidenceView{Evidence: evidence}
	if root == "" {
		view.State = model.EvidenceUnavailable
		view.Detail = "repository metadata is unavailable, so the file cannot be compared"
		return view, nil
	}
	recorded, err := gitrepo.ShowFile(ctx, root, evidence.Commit, evidence.Path)
	if ctx.Err() != nil {
		return view, ctx.Err()
	}
	switch {
	case errors.Is(err, gitrepo.ErrUnknownCommit):
		view.State = model.EvidenceUnavailable
		view.Detail = fmt.Sprintf("recorded commit %q cannot be read from this checkout", evidence.Commit)
		return view, nil
	case errors.Is(err, os.ErrNotExist):
		view.State = model.EvidenceUnavailable
		view.Detail = fmt.Sprintf("recorded path %q is absent at commit %q", evidence.Path, evidence.Commit)
		return view, nil
	case err != nil:
		view.State = model.EvidenceUnavailable
		view.Detail = "the recorded commit content could not be read"
		return view, nil
	}
	recordedSelected, err := selectEvidenceLine(recorded, evidence.Line)
	if err != nil {
		view.State = model.EvidenceUnavailable
		view.Detail = fmt.Sprintf("recorded line %d is beyond the recorded content: %v", evidence.Line, err)
		return view, nil
	}
	if digestEvidenceContent(recordedSelected) != evidence.Blob {
		view.State = model.EvidenceUnavailable
		view.Detail = "the recorded digest does not match the recorded commit content"
		return view, nil
	}
	current, err := gitrepo.ReadWorkingFile(ctx, root, evidence.Path)
	if ctx.Err() != nil {
		return view, ctx.Err()
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		view.State = model.EvidenceMissing
		view.Detail = fmt.Sprintf("path %q is absent from the working tree", evidence.Path)
		return view, nil
	case errors.Is(err, gitrepo.ErrNotRegularFile):
		view.State = model.EvidenceUnavailable
		view.Detail = fmt.Sprintf("path %q is not a regular file", evidence.Path)
		return view, nil
	case err != nil:
		view.State = model.EvidenceUnavailable
		view.Detail = "the working-tree file could not be read"
		return view, nil
	}
	currentSelected, err := selectEvidenceLine(current, evidence.Line)
	if err != nil {
		view.State = model.EvidenceMissing
		view.Detail = fmt.Sprintf("line %d is absent from the current file", evidence.Line)
		return view, nil
	}
	if digestEvidenceContent(currentSelected) == evidence.Blob {
		view.State = model.EvidenceUnchanged
		if evidence.Line == 0 {
			view.Detail = "the file content still matches the recorded digest"
		} else {
			view.Detail = fmt.Sprintf("line %d still matches the recorded digest", evidence.Line)
		}
		return view, nil
	}
	view.State = model.EvidenceChanged
	if evidence.Line == 0 {
		view.Detail = "the file content differs from the recorded digest"
	} else {
		view.Detail = fmt.Sprintf("line %d differs from the recorded digest", evidence.Line)
	}
	return view, nil
}

// captureEvidenceDigest reads the selected path/line from the given commit
// and returns its stable digest. The working tree is never consulted, so
// dirty bytes are never recorded as evidence for an unchanged commit.
func captureEvidenceDigest(ctx context.Context, root, commit, path string, line int) (string, error) {
	if strings.TrimSpace(commit) == "" {
		return "", fmt.Errorf("%w: evidence requires a commit; the repository has none", model.ErrInvalidInput)
	}
	content, err := gitrepo.ShowFile(ctx, root, commit, path)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	switch {
	case errors.Is(err, gitrepo.ErrUnknownCommit):
		return "", fmt.Errorf("%w: commit %q cannot be read", model.ErrInvalidInput, commit)
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: evidence path %s does not exist at commit %s", model.ErrInvalidInput, path, commit)
	case err != nil:
		return "", err
	}
	selected, err := selectEvidenceLine(content, line)
	if err != nil {
		return "", fmt.Errorf("%w: %v", model.ErrInvalidInput, err)
	}
	return digestEvidenceContent(selected), nil
}
