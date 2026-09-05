// Package git provides the small amount of repository metadata Elephant needs.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotGitRepository reports that the requested working directory is not a
// Git work tree.
var ErrNotGitRepository = errors.New("not a git repository")

// Metadata describes the Git state visible from a working directory.
type Metadata struct {
	Root     string `json:"root"`
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Remote   string `json:"remote"`
	Head     string `json:"head"`
	Branch   string `json:"branch"`
	Dirty    bool   `json:"dirty"`
}

// Inspect reads repository metadata from cwd. cwd may point at any directory
// below the repository root, including a linked worktree.
//
// An unborn repository has no commit to report, so Head is empty in that
// state. A detached HEAD has no branch name, so Branch is empty.
func Inspect(ctx context.Context, cwd string) (Metadata, error) {
	rootValue, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		if isNotRepository(err) {
			return Metadata{}, fmt.Errorf("%w: %v", ErrNotGitRepository, err)
		}
		return Metadata{}, err
	}
	root, err := absolutePath(rootValue, cwd)
	if err != nil {
		return Metadata{}, fmt.Errorf("resolve repository root: %w", err)
	}

	remote, err := runGit(ctx, cwd, "config", "--get", "remote.origin.url")
	if err != nil {
		if !isMissingRemote(err) {
			return Metadata{}, err
		}
		remote = ""
	}
	remote = sanitizeRemote(remote)

	head, err := runGit(ctx, cwd, "rev-parse", "--verify", "HEAD")
	if err != nil {
		if !isUnbornHead(ctx, cwd) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Metadata{}, ctxErr
			}
			return Metadata{}, err
		}
		head = ""
	}

	branch, err := runGit(ctx, cwd, "branch", "--show-current")
	if err != nil {
		return Metadata{}, err
	}

	status, err := runGit(ctx, cwd, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return Metadata{}, err
	}

	identity, name, err := projectIdentity(ctx, cwd, root, remote)
	if err != nil {
		return Metadata{}, err
	}

	return Metadata{
		Root:     root,
		Identity: identity,
		Name:     name,
		Remote:   remote,
		Head:     head,
		Branch:   branch,
		Dirty:    status != "",
	}, nil
}

// NormalizeRemote returns the canonical host/path identity for a Git remote.
// It accepts SSH scp syntax and URL forms. User information, query strings,
// and fragments are omitted so credentials cannot become part of an identity.
func NormalizeRemote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("invalid empty or multiline Git remote")
	}

	host, path, ok := parseScpRemote(raw)
	if !ok {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", errors.New("invalid Git remote URL")
		}
		scheme := strings.ToLower(parsed.Scheme)
		switch scheme {
		case "ssh", "https", "http", "git", "git+ssh":
		default:
			return "", errors.New("unsupported Git remote URL scheme")
		}
		if parsed.Host == "" || parsed.Hostname() == "" {
			return "", errors.New("Git remote URL has no host")
		}

		host = strings.ToLower(parsed.Hostname())
		if port := parsed.Port(); port != "" {
			host += ":" + port
		}
		path = parsed.Path
	}

	path = strings.TrimRight(path, "/")
	path = strings.TrimLeft(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimRight(path, "/")
	if host == "" || path == "" {
		return "", errors.New("Git remote has no repository path")
	}

	return host + "/" + path, nil
}

type commandError struct {
	args   []string
	err    error
	stderr string
}

func (e *commandError) Error() string {
	message := fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	if e.stderr != "" {
		message += ": " + strings.TrimSpace(e.stderr)
	}
	return message
}

func (e *commandError) Unwrap() error { return e.err }

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = cwd
	command.Env = withoutRepositoryLocationEnv()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return strings.TrimSpace(string(output)), &commandError{
			args:   append([]string(nil), args...),
			err:    err,
			stderr: stderr.String(),
		}
	}
	return strings.TrimSpace(string(output)), nil
}

func withoutRepositoryLocationEnv() []string {
	const (
		gitDir                 = "GIT_DIR"
		gitWorkTree            = "GIT_WORK_TREE"
		gitCommonDir           = "GIT_COMMON_DIR"
		gitIndexFile           = "GIT_INDEX_FILE"
		gitObjectDirectory     = "GIT_OBJECT_DIRECTORY"
		gitAlternateObjectDirs = "GIT_ALTERNATE_OBJECT_DIRECTORIES"
		gitCeilingDirectories  = "GIT_CEILING_DIRECTORIES"
	)
	blocked := map[string]struct{}{
		gitDir: {}, gitWorkTree: {}, gitCommonDir: {}, gitIndexFile: {},
		gitObjectDirectory: {}, gitAlternateObjectDirs: {}, gitCeilingDirectories: {},
	}
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := blocked[key]; !ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func isNotRepository(err error) bool {
	var commandErr *commandError
	if !errors.As(err, &commandErr) {
		return false
	}
	message := strings.ToLower(commandErr.stderr)
	return strings.Contains(message, "not a git repository") ||
		strings.Contains(message, "outside a repository") ||
		strings.Contains(message, "must be run in a work tree")
}

func isMissingRemote(err error) bool {
	var commandErr *commandError
	if !errors.As(err, &commandErr) {
		return false
	}
	return commandErr.stderr == "" && exitCode(commandErr.err) == 1
}

func isUnbornHead(ctx context.Context, cwd string) bool {
	_, err := runGit(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
	return err == nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func absolutePath(value, cwd string) (string, error) {
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(cwdAbs, value)), nil
}

func projectIdentity(ctx context.Context, cwd, root, remote string) (string, string, error) {
	if remote != "" {
		if localPath, ok, err := normalizeLocalRemote(remote, root); ok {
			if err != nil {
				return "", "", fmt.Errorf("normalize local origin remote: %w", err)
			}
			return "local:" + localPath, filepath.Base(localPath), nil
		}
		identity, err := NormalizeRemote(remote)
		if err != nil {
			return "", "", fmt.Errorf("normalize origin remote: %w", err)
		}
		return identity, repositoryName(identity, root), nil
	}

	commonDirValue, err := runGit(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", ctxErr
		}
		// --path-format was added after --git-common-dir. Keep a fallback for
		// older Git versions, whose relative result is relative to cwd.
		commonDirValue, err = runGit(ctx, cwd, "rev-parse", "--git-common-dir")
		if err != nil {
			return "", "", err
		}
	}
	commonDir, err := absolutePath(commonDirValue, cwd)
	if err != nil {
		return "", "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(commonDir); resolveErr == nil {
		commonDir = resolved
	}
	commonRoot := commonDir
	if filepath.Base(commonDir) == ".git" {
		commonRoot = filepath.Dir(commonDir)
	}
	identity := "local:" + commonRoot
	return identity, repositoryName(identity, root), nil
}

func normalizeLocalRemote(raw, root string) (path string, ok bool, err error) {
	if raw == "" {
		return "", false, nil
	}

	if strings.HasPrefix(strings.ToLower(raw), "file:") {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || strings.ToLower(parsed.Scheme) != "file" {
			return "", true, errors.New("invalid local file remote")
		}
		path = parsed.Path
		if path == "" {
			path = parsed.Opaque
		}
		if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			path = "//" + parsed.Host + "/" + strings.TrimLeft(path, "/")
		}
		if path == "" {
			return "", true, errors.New("local file remote has no path")
		}
		return resolveLocalPath(path, root), true, nil
	}

	parsed, parseErr := url.Parse(raw)
	if parseErr != nil {
		return "", false, nil
	}
	if parsed.Scheme != "" || parseScpIsRemote(raw) {
		return "", false, nil
	}
	return resolveLocalPath(raw, root), true, nil
}

func resolveLocalPath(path, root string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}

func repositoryName(identity, root string) string {
	if slash := strings.LastIndexByte(identity, '/'); slash >= 0 && slash+1 < len(identity) {
		return identity[slash+1:]
	}
	return filepath.Base(root)
}

func parseScpRemote(raw string) (host, path string, ok bool) {
	colon := strings.IndexByte(raw, ':')
	if colon <= 0 || strings.Contains(raw[:colon], "/") || strings.Contains(raw[:colon], "\\") {
		return "", "", false
	}
	// A URL scheme has a colon too, but is handled by net/url below.
	if strings.Contains(raw, "://") {
		return "", "", false
	}
	userHost := raw[:colon]
	if at := strings.LastIndexByte(userHost, '@'); at >= 0 {
		userHost = userHost[at+1:]
	}
	return strings.ToLower(userHost), raw[colon+1:], true
}

func parseScpIsRemote(raw string) bool {
	_, _, ok := parseScpRemote(raw)
	return ok
}

func sanitizeRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, path, ok := parseScpRemote(raw); ok {
		userHost := raw[:strings.IndexByte(raw, ':')]
		if at := strings.LastIndexByte(userHost, '@'); at >= 0 {
			username := userHost[:at]
			if colon := strings.IndexByte(username, ':'); colon >= 0 {
				username = username[:colon]
			}
			userHost = username + "@" + host
		} else {
			userHost = host
		}
		return userHost + ":" + stripRemoteQuery(path)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	switch strings.ToLower(parsed.Scheme) {
	case "ssh", "git+ssh":
		if parsed.User != nil {
			parsed.User = url.User(parsed.User.Username())
		}
	case "http", "https", "file", "git":
		parsed.User = nil
	}
	return parsed.String()
}

func stripRemoteQuery(path string) string {
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		return path[:index]
	}
	return path
}
