package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnknownCommit reports that a recorded commit cannot be read from the
// local object store, so evidence pinned to it cannot be verified.
var ErrUnknownCommit = errors.New("unknown commit")

// VerifyCommit reports whether commit resolves to a commit object in the
// repository at root. Unknown revisions wrap ErrUnknownCommit.
func VerifyCommit(ctx context.Context, root, commit string) error {
	if strings.TrimSpace(commit) == "" {
		return fmt.Errorf("%w: empty commit", ErrUnknownCommit)
	}
	if _, err := runGit(ctx, root, "cat-file", "-e", commit+"^{commit}"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnknownCommit, commit)
	}
	return nil
}

// ShowFile returns the bytes of path as recorded in commit. A caller-provided
// repository-relative path is resolved against root. An unreadable commit
// wraps ErrUnknownCommit; a path absent at that commit wraps os.ErrNotExist.
func ShowFile(ctx context.Context, root, commit, path string) ([]byte, error) {
	if strings.TrimSpace(commit) == "" {
		return nil, fmt.Errorf("%w: empty commit", ErrUnknownCommit)
	}
	// Confirm the commit first so an unknown revision and a missing path
	// produce distinct, stable errors across Git versions.
	if err := VerifyCommit(ctx, root, commit); err != nil {
		return nil, err
	}
	out, err := runGitBytes(ctx, root, "show", commit+":"+filepath.ToSlash(path))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s not present at %s", os.ErrNotExist, path, commit)
	}
	return out, nil
}

// ReadWorkingFile returns the bytes of path in the working tree. A missing
// file returns an error wrapping os.ErrNotExist; a non-regular file wraps
// ErrNotRegularFile.
func ReadWorkingFile(_ context.Context, root, path string) ([]byte, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegularFile, path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return data, nil
}
