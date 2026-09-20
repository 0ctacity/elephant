package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RootsLister supplies the client roots currently advertised to the server.
// It is a function so scope resolution stays testable without a live session.
type RootsLister func(ctx context.Context) (*sdk.ListRootsResult, error)

// SessionRoots adapts a server session to a RootsLister. A nil session yields
// nil, which resolves to the default. ListRoots failures inside request
// handlers (the SDK forbids server-initiated requests while serving one, and
// roots are deprecated upstream) also fall back to the default.
func SessionRoots(session *sdk.ServerSession) RootsLister {
	if session == nil {
		return nil
	}
	return func(ctx context.Context) (*sdk.ListRootsResult, error) {
		return session.ListRoots(ctx, nil)
	}
}

// ResolveCWD selects the repository for one operation. An explicit cwd always
// wins, then the first usable file:// root, then the server default. Roots
// never cross project boundaries on their own: they only select which local
// repository Elephant resolves, and every operation stays project-scoped.
func ResolveCWD(ctx context.Context, explicit string, listRoots RootsLister, def string) string {
	if explicit != "" {
		return explicit
	}
	if listRoots == nil {
		return def
	}
	res, err := listRoots(ctx)
	if err != nil || res == nil {
		return def
	}
	if path, ok := FirstFileRoot(res.Roots); ok {
		return path
	}
	return def
}

// FirstFileRoot returns the first root convertible to a local path, skipping
// invalid URIs and non-file schemes.
func FirstFileRoot(roots []*sdk.Root) (string, bool) {
	for _, r := range roots {
		if r == nil {
			continue
		}
		if path, err := FileURIToPath(r.URI); err == nil {
			return path, true
		}
	}
	return "", false
}

// FileURIToPath converts a file:// root URI to a local path on Unix and
// Windows, independent of the host OS so behavior is testable everywhere:
//
//	file:///repo/share      -> /repo/share
//	file:///C:/repo/share   -> C:/repo/share (forward slashes work in Go on Windows)
//	file://host/share/dir   -> //host/share/dir (UNC)
//	file://localhost/C|/x   -> C:/x
//
// Percent encoding is decoded. Non-file schemes, unparseable URIs, and empty
// paths are rejected.
func FileURIToPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid root URI %q: %v", raw, err)
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("unsupported root scheme %q (want file)", u.Scheme)
	}
	path := u.Path
	if u.Opaque != "" && path == "" {
		path = u.Opaque
	}
	if path == "" {
		return "", fmt.Errorf("root URI %q has no path", raw)
	}
	host := u.Hostname()
	if host != "" && !strings.EqualFold(host, "localhost") {
		// Network share: keep UNC form with forward slashes.
		return "//" + host + path, nil
	}
	// url.Parse keeps "file:///C:/x" as path "/C:/x"; strip the slash so
	// the drive letter survives on every platform. The legacy "C|/x"
	// drive form (slash-prefixed or opaque) maps to "C:/x".
	if len(path) >= 3 && path[0] == '/' && isDriveLetter(path[1]) && (path[2] == ':' || path[2] == '|') {
		path = string(path[1]) + ":" + path[3:]
	} else if len(path) >= 3 && isDriveLetter(path[0]) && path[1] == '|' {
		path = string(path[0]) + ":" + path[2:]
	}
	return path, nil
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
