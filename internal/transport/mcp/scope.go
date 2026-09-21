package mcp

import (
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// rootsInputRequestID is the request ID Elephant uses when a handler needs
// the client's advertised roots through the multi round-trip flow.
const rootsInputRequestID = "client_roots"

// clientRootsCapable reports whether the session's client declared the roots
// capability at initialization. Sessions without completed initialization
// (including test doubles without one) resolve to the server default.
func clientRootsCapable(session *sdk.ServerSession) bool {
	if session == nil {
		return false
	}
	iparams := session.InitializeParams()
	if iparams == nil || iparams.Capabilities == nil {
		return false
	}
	return iparams.Capabilities.RootsV2 != nil || iparams.Capabilities.Roots.ListChanged
}

// RootsInputRequests returns the input request that asks the client for its
// advertised roots (SEP-2322). A resources/read or prompts/get handler
// returns it with no content while serving a request, because the negotiated
// protocol (2026-07-28, SEP-2322) forbids server-initiated roots/list there
// and roots are deprecated upstream (SEP-2577). The SDK fulfills the request
// — through the client on multi-round-trip protocol versions, and through a
// transparent server-initiated list on older ones — and reinvokes the
// handler with the roots echoed back in InputResponses.
func RootsInputRequests() sdk.InputRequestMap {
	return sdk.InputRequestMap{rootsInputRequestID: &sdk.ListRootsParams{}}
}

// RootsFromInputResponses returns the client roots echoed back on a
// multi round-trip retry, or nil when the response is absent or malformed.
func RootsFromInputResponses(responses sdk.InputResponseMap) *sdk.ListRootsResult {
	if len(responses) == 0 {
		return nil
	}
	res, _ := responses[rootsInputRequestID].(*sdk.ListRootsResult)
	return res
}

// RequestScope resolves the repository for a resources/read or prompts/get
// request. An explicit cwd always wins. Otherwise the client's advertised
// roots select the first usable file:// root, with the server directory as
// fallback when the client never advertised roots or none of its roots are
// usable. Roots never cross project boundaries on their own: they only
// select which local repository Elephant resolves, and every operation stays
// project-scoped.
//
// needRoots is returned with an empty cwd when the handler must return
// RootsInputRequests() instead of a result; the roots/list exchange then
// repeats the request with the client's answer in InputResponses.
func RequestScope(session *sdk.ServerSession, explicit string, responses sdk.InputResponseMap, def string) (cwd string, needRoots bool) {
	if explicit != "" {
		return explicit, false
	}
	if !clientRootsCapable(session) {
		return def, false
	}
	if len(responses) == 0 {
		return "", true
	}
	if res := RootsFromInputResponses(responses); res != nil {
		if path, ok := FirstFileRoot(res.Roots); ok {
			return path, false
		}
	}
	return def, false
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
