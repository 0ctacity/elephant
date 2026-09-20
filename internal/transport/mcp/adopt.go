package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
)

type diffInput struct {
	scope
	Remote string `json:"remote" jsonschema:"Registered peer name or source Elephant UUID"`
}

type adoptInput struct {
	scope
	ID     string `json:"id" jsonschema:"Remote entry ID to adopt"`
	Remote string `json:"remote" jsonschema:"Registered peer name or source Elephant UUID"`
}

func registerAdopt(s *sdk.Server, service *app.Service, cwd func(string) string) {
	register(s, "remote_inbox", "List received additions grouped by source Elephant and project, never merging sources.", func(ctx context.Context, in scope) ([]app.InboxGroup, error) {
		return service.Inbox(ctx)
	})
	register(s, "remote_diff", "Compare one remote source table with local state while preserving both boundaries.", func(ctx context.Context, in diffInput) (app.DiffResult, error) {
		return service.Diff(ctx, cwd(in.CWD), in.Remote)
	})
	register(s, "adopt_entry", "Explicitly promote one received entry into local truth with provenance; idempotent on repeat.", func(ctx context.Context, in adoptInput) (app.AdoptResult, error) {
		return service.Adopt(ctx, cwd(in.CWD), in.ID, in.Remote)
	})
}
