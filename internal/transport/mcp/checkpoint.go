package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
	"elephant/internal/model"
)

type checkpointInput struct {
	scope
	app.CheckpointInput
}

type checkpointsInput struct {
	scope
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

func registerCheckpoints(s *sdk.Server, service *app.Service, cwd func(string) string) {
	register(s, "add_checkpoint", "Record a session boundary with summary, completed work, next actions, commands run, and failures; links to entries and files are atomic.", func(ctx context.Context, in checkpointInput) (app.CheckpointRecord, error) {
		return service.AddCheckpoint(ctx, cwd(in.CWD), in.CheckpointInput)
	})
	register(s, "list_checkpoints", "List session checkpoints newest first with bounded pagination.", func(ctx context.Context, in checkpointsInput) ([]model.Checkpoint, error) {
		return service.ListCheckpoints(ctx, cwd(in.CWD), in.Limit, in.Offset)
	})
}
