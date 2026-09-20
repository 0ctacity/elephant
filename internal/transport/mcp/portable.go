package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/model"
)

type importInput struct {
	scope
	Envelope app.ExportEnvelope `json:"envelope"`
	AsRemote string             `json:"as_remote,omitempty"`
}

func registerPortable(s *sdk.Server, service *app.Service, cwd func(string) string) {
	register(s, "export_project", "Export the current project's memory as a versioned, deterministic envelope.", func(ctx context.Context, in scope) (app.ExportEnvelope, error) {
		return service.Export(ctx, cwd(in.CWD))
	})
	register(s, "import_project", "Validate an export envelope fully before storing it in a separate archive source table; never overwrites local truth.", func(ctx context.Context, in importInput) (model.Project, error) {
		return service.Import(ctx, in.Envelope, in.AsRemote)
	})
}
