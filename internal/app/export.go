package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	gitrepo "elephant/internal/git"
	"elephant/internal/model"
	"elephant/internal/storage"
)

// ExportFormatVersion is the only understood envelope version. Newer versions
// fail explicitly rather than importing partially. Version 2 covers the
// complete project state: entries, relations, evidence, checkpoints, and
// adoption provenance with their metadata. Version 1 envelopes (entries and
// relations only) remain readable on import.
const ExportFormatVersion = 2

// ExportEnvelope is the portable, inspectable backup of one project's memory.
// It carries global IDs and every stored record class so a backup restores a
// byte-comparable project state elsewhere. No wall-clock export time is
// recorded, so identical stored state serializes identically.
type ExportEnvelope struct {
	FormatVersion int                `json:"format_version"`
	Project       ProjectRef         `json:"project"`
	Entries       []model.Entry      `json:"entries"`
	Relations     []model.Relation   `json:"relations"`
	Evidence      []model.Evidence   `json:"evidence"`
	Checkpoints   []model.Checkpoint `json:"checkpoints"`
	Adoptions     []model.Adoption   `json:"adoptions"`
}

// Export returns a deterministic snapshot of the local project: entries sorted
// by ID, relations sorted by from/type/to, evidence sorted by entry/created/ID,
// checkpoints sorted by creation order, and adoption receipts sorted by
// source/entry. Actors, lifecycle fields (status, target version, start and end
// commits), and timestamps ride inside the records themselves.
func (s *Service) Export(ctx context.Context, cwd string) (out ExportEnvelope, err error) {
	g, err := gitrepo.Inspect(ctx, cwd)
	if err != nil {
		return out, err
	}
	out.FormatVersion = ExportFormatVersion
	out.Project = ProjectRef{Identity: g.Identity, Name: g.Name}
	out.Entries = []model.Entry{}
	out.Relations = []model.Relation{}
	out.Evidence = []model.Evidence{}
	out.Checkpoints = []model.Checkpoint{}
	out.Adoptions = []model.Adoption{}
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		local, err := resolve(tx, g)
		if err != nil {
			return err
		}
		offset := 0
		for {
			page, err := tx.List(local, model.Filter{Limit: 200, Offset: offset})
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			out.Entries = append(out.Entries, page...)
			offset += len(page)
			if len(page) < 200 {
				break
			}
		}
		seen := map[string]bool{}
		for _, e := range out.Entries {
			links, err := tx.Relations(model.EntryNode(local, e.ID))
			if err != nil {
				return err
			}
			for _, r := range links {
				key := r.From + "\x00" + r.Type + "\x00" + r.To
				if !seen[key] {
					seen[key] = true
					out.Relations = append(out.Relations, r)
				}
			}
			stored, err := tx.Evidence(local, e.ID)
			if err != nil {
				return err
			}
			out.Evidence = append(out.Evidence, stored...)
		}
		for offset := 0; ; offset += 200 {
			page, err := tx.ListCheckpoints(local, 200, offset)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			out.Checkpoints = append(out.Checkpoints, page...)
			if len(page) < 200 {
				break
			}
		}
		sources, err := tx.Sources()
		if err != nil {
			return err
		}
		for _, src := range sources {
			receipts, err := tx.AdoptionsBySource(g.Identity, src.SourceElephantID)
			if err != nil {
				return err
			}
			out.Adoptions = append(out.Adoptions, receipts...)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	sortExportEnvelope(&out)
	return out, nil
}

func sortExportEnvelope(env *ExportEnvelope) {
	sort.Slice(env.Entries, func(i, j int) bool { return env.Entries[i].ID < env.Entries[j].ID })
	sort.Slice(env.Relations, func(i, j int) bool {
		a, b := env.Relations[i], env.Relations[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.To < b.To
	})
	sort.Slice(env.Evidence, func(i, j int) bool {
		a, b := env.Evidence[i], env.Evidence[j]
		if a.EntryID != b.EntryID {
			return a.EntryID < b.EntryID
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	// Checkpoints are UUIDv7 IDs, so ID order is creation order and stable
	// across exports.
	sort.Slice(env.Checkpoints, func(i, j int) bool { return env.Checkpoints[i].ID < env.Checkpoints[j].ID })
	sort.Slice(env.Adoptions, func(i, j int) bool {
		a, b := env.Adoptions[i], env.Adoptions[j]
		if a.SourceElephantID != b.SourceElephantID {
			return a.SourceElephantID < b.SourceElephantID
		}
		return a.SourceEntryID < b.SourceEntryID
	})
}

// exportContentDigest canonically digests the record sections of an envelope,
// so identical content yields an identical key regardless of JSON ordering.
func exportContentDigest(entries []model.Entry, relations []model.Relation) (string, error) {
	b, err := json.Marshal(struct {
		Entries   []model.Entry    `json:"entries"`
		Relations []model.Relation `json:"relations"`
	}{entries, relations})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// decodeExportEnvelope strictly decodes one JSON export: unknown fields and
// trailing data are rejected so malformed archives never import partially.
// Version 1 archives (entries and relations only) are accepted and upgraded
// in memory; version 2 must carry every section.
func decodeExportEnvelope(data []byte) (ExportEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var env ExportEnvelope
	if err := decoder.Decode(&env); err != nil {
		return env, fmt.Errorf("%w: invalid export JSON: %v", model.ErrInvalidInput, err)
	}
	if decoder.More() {
		return env, fmt.Errorf("%w: trailing data after export JSON", model.ErrInvalidInput)
	}
	if env.FormatVersion == 1 {
		env.Evidence = []model.Evidence{}
		env.Checkpoints = []model.Checkpoint{}
		env.Adoptions = []model.Adoption{}
	}
	return env, nil
}

// Import validates an envelope fully before committing anything, then stores
// it in a separate remote/archive source table so local truth is never
// overwritten. Reimporting the same archive is idempotent: an identical
// archive identity and content digest returns the existing source, while the
// same name with conflicting content fails explicitly. Validation failures
// leave no partial rows, links, evidence, checkpoints, or receipts.
func (s *Service) Import(ctx context.Context, env ExportEnvelope, asRemote string) (out model.Project, err error) {
	if env.FormatVersion != ExportFormatVersion && env.FormatVersion != 1 {
		return out, fmt.Errorf("%w: unsupported export format version %d (expected %d)", model.ErrInvalidInput, env.FormatVersion, ExportFormatVersion)
	}
	if asRemote == "" {
		asRemote = "archive"
	}
	if !validText(asRemote, 100) || validID(asRemote) {
		return out, fmt.Errorf("%w: archive name required (not a UUID)", model.ErrInvalidInput)
	}
	if !validText(env.Project.Identity, 2048) || !validText(env.Project.Name, 300) {
		return out, fmt.Errorf("%w: export project identity required", model.ErrInvalidInput)
	}
	if len(env.Entries) > 10000 || len(env.Relations) > 20000 || len(env.Evidence) > 500000 || len(env.Checkpoints) > 10000 || len(env.Adoptions) > 10000 {
		return out, fmt.Errorf("%w: export too large", model.ErrInvalidInput)
	}
	// Full validation before any mutation.
	ids := map[string]model.Entry{}
	for _, e := range env.Entries {
		if !validID(e.ID) {
			return out, fmt.Errorf("%w: entry ID must be UUIDv7", model.ErrInvalidInput)
		}
		if _, dup := ids[e.ID]; dup {
			return out, fmt.Errorf("%w: duplicate entry ID", model.ErrInvalidInput)
		}
		if err := e.Validate(); err != nil {
			return out, err
		}
		if e.CreatedAt.IsZero() || e.UpdatedAt.Before(e.CreatedAt) {
			return out, fmt.Errorf("%w: entry timestamps required", model.ErrInvalidInput)
		}
		if !validText(e.ActorID, 300) {
			return out, fmt.Errorf("%w: actor ID required", model.ErrInvalidInput)
		}
		ids[e.ID] = e
	}
	for _, r := range env.Relations {
		fromID, ok := entryIDFromNode(r.From)
		if !ok {
			return out, fmt.Errorf("%w: invalid relation source", model.ErrInvalidInput)
		}
		from, ok := ids[fromID]
		if !ok {
			return out, fmt.Errorf("%w: relation source unknown", model.ErrInvalidInput)
		}
		if isFileNode(r.To) {
			file := filePathFromNode(r.To)
			if _, err := model.CleanFile(file); err != nil {
				return out, err
			}
			if r.Type != model.FileEdge(from.Kind) {
				return out, fmt.Errorf("%w: invalid file edge", model.ErrInvalidInput)
			}
			continue
		}
		toID, ok := entryIDFromNode(r.To)
		if !ok {
			return out, fmt.Errorf("%w: invalid relation target", model.ErrInvalidInput)
		}
		to, ok := ids[toID]
		if !ok {
			return out, fmt.Errorf("%w: relation target unknown", model.ErrInvalidInput)
		}
		if err := model.ValidateRelation(from.Kind, r.Type, to.Kind); err != nil {
			return out, err
		}
	}
	evidenceByEntry := map[string][]model.Evidence{}
	evidenceIDs := map[string]bool{}
	for _, ev := range env.Evidence {
		if !validID(ev.ID) {
			return out, fmt.Errorf("%w: evidence ID must be UUIDv7", model.ErrInvalidInput)
		}
		if _, dup := evidenceIDs[ev.ID]; dup {
			return out, fmt.Errorf("%w: duplicate evidence ID", model.ErrInvalidInput)
		}
		if err := ev.Validate(); err != nil {
			return out, err
		}
		if _, ok := ids[ev.EntryID]; !ok {
			return out, fmt.Errorf("%w: evidence targets unknown entry", model.ErrInvalidInput)
		}
		evidenceIDs[ev.ID] = true
		evidenceByEntry[ev.EntryID] = append(evidenceByEntry[ev.EntryID], ev)
	}
	for _, rows := range evidenceByEntry {
		if len(rows) > MaxEvidencePerEntry {
			return out, fmt.Errorf("%w: at most %d evidence locations per entry", model.ErrInvalidInput, MaxEvidencePerEntry)
		}
	}
	checkpointIDs := map[string]bool{}
	for _, c := range env.Checkpoints {
		if !validID(c.ID) {
			return out, fmt.Errorf("%w: checkpoint ID must be UUIDv7", model.ErrInvalidInput)
		}
		if _, dup := checkpointIDs[c.ID]; dup {
			return out, fmt.Errorf("%w: duplicate checkpoint ID", model.ErrInvalidInput)
		}
		if err := c.Validate(); err != nil {
			return out, err
		}
		if !validText(c.ActorID, 300) {
			return out, fmt.Errorf("%w: checkpoint actor ID required", model.ErrInvalidInput)
		}
		checkpointIDs[c.ID] = true
	}
	adoptKeys := map[string]bool{}
	for _, a := range env.Adoptions {
		if !validID(a.ID) || !validID(a.SourceEntryID) || !validID(a.LocalEntryID) {
			return out, fmt.Errorf("%w: adoption IDs must be UUIDv7", model.ErrInvalidInput)
		}
		if a.ProjectIdentity != env.Project.Identity {
			return out, fmt.Errorf("%w: adoption provenance from a foreign project", model.ErrInvalidInput)
		}
		if _, ok := ids[a.LocalEntryID]; !ok {
			return out, fmt.Errorf("%w: adoption references unknown local entry", model.ErrInvalidInput)
		}
		if validID(a.SourceElephantID) && a.SourceElephantID == a.LocalEntryID {
			return out, fmt.Errorf("%w: adoption provenance is self-referential", model.ErrInvalidInput)
		}
		adoptKeys[a.SourceElephantID+"\x00"+a.SourceEntryID] = true
	}
	// Idempotency: an identical reimport returns the existing source; a
	// conflicting content digest under the same archive name fails loudly.
	sourceID := "import:" + asRemote
	archiveDigest := digest(struct {
		Project     ProjectRef         `json:"project"`
		Entries     []model.Entry      `json:"entries"`
		Relations   []model.Relation   `json:"relations"`
		Evidence    []model.Evidence   `json:"evidence"`
		Checkpoints []model.Checkpoint `json:"checkpoints"`
		Adoptions   []model.Adoption   `json:"adoptions"`
	}{env.Project, env.Entries, env.Relations, env.Evidence, env.Checkpoints, env.Adoptions})
	err = s.store.Transact(ctx, func(tx storage.Tx) error {
		seen, err := tx.Message(sourceID, archiveDigest, "import")
		if err != nil {
			return err
		}
		if seen {
			out, err = tx.Source(env.Project.Identity, sourceID)
			return err
		}
		src, err := tx.EnsureSource(model.Project{Identity: env.Project.Identity, Name: env.Project.Name}, sourceID)
		if err != nil {
			return err
		}
		out = src
		for _, e := range env.Entries {
			if err = tx.Put(src, e); err != nil {
				return err
			}
		}
		for _, r := range env.Relations {
			fromID, _ := entryIDFromNode(r.From)
			toRewrite := r
			toRewrite.From = model.EntryNode(src, fromID)
			if !isFileNode(r.To) {
				toID, _ := entryIDFromNode(r.To)
				toRewrite.To = model.EntryNode(src, toID)
			} else {
				toRewrite.To = "file:" + src.ID + ":" + filePathFromNode(r.To)
			}
			if err = tx.Link(src, toRewrite); err != nil {
				return err
			}
		}
		for _, ev := range env.Evidence {
			if err = tx.PutEvidence(src, ev); err != nil {
				return err
			}
		}
		for _, c := range env.Checkpoints {
			if err = tx.PutCheckpoint(src, c); err != nil {
				println("DEBUG PutCheckpoint failed:", err.Error())
				return err
			}
			println("DEBUG PutCheckpoint ok:", c.ID, "table:", src.TableName)
		}
		for _, a := range env.Adoptions {
			if err = tx.RecordAdoption(a); err != nil {
				return err
			}
		}
		return nil
	})
	return
}

func entryIDFromNode(n string) (string, bool) {
	after, ok := strings.CutPrefix(n, "entry:")
	if !ok {
		return "", false
	}
	// Local form entry:<uuid>; remote form entry:<table>:<uuid>.
	if i := strings.LastIndex(after, ":"); i >= 0 {
		after = after[i+1:]
	}
	return after, validID(after)
}

func isFileNode(n string) bool { return strings.HasPrefix(n, "file:") }

func filePathFromNode(n string) string {
	rest := strings.TrimPrefix(n, "file:")
	// Exported file nodes are file:<projectID>:<path>; be tolerant of legacy forms.
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// Backup stages a storage-safe snapshot in a sibling temp file and renames it
// into place only on success. A failed backup preserves any existing
// destination and leaves no temp litter. The rename replaces existing
// destinations on Unix and on Windows: os.Rename maps to MoveFileEx with
// MOVEFILE_REPLACE_EXISTING, and a lingering read handle from a concurrent
// reader is resolved by one bounded replace window before failing.
func (s *Service) Backup(ctx context.Context, dest string) error {
	_ = ctx
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*.zova")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Close(); err != nil {
		return err
	}
	// Zova requires a .zova destination and refuses any existing file, so the
	// reserved name is removed before the store writes the snapshot.
	if err = os.Remove(tmpName); err != nil {
		return err
	}
	if err = s.store.Backup(tmpName); err != nil {
		return err
	}
	if err = replaceFile(tmpName, dest); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

// replaceFile moves src onto dest, replacing any existing destination. On
// Unix os.Rename already does this. On Windows os.Rename maps to MoveFileEx
// with MOVEFILE_REPLACE_EXISTING, which fails while another handle still has
// the destination open with sharing restrictions; a short retry window lets a
// concurrent reader finish so repeated backups can safely replace the
// destination on Windows as well as Unix.
func replaceFile(src, dest string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dest)
	}
	const attempts = 10
	const delay = 100 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = os.Rename(src, dest); err == nil {
			return nil
		}
		var pathErr *os.LinkError
		if !errors.As(err, &pathErr) {
			break
		}
		time.Sleep(delay)
	}
	return fmt.Errorf("%w: %w", model.ErrStorage, err)
}
