package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"elephant/internal/app"
)

type reviewInput struct {
	scope
	StaleTaskDays      int `json:"stale_task_days,omitempty"`
	UnverifiedFactDays int `json:"unverified_fact_days,omitempty"`
	StaleRemoteDays    int `json:"stale_remote_days,omitempty"`
}

func registerReview(s *sdk.Server, service *app.Service, cwd func(string) string) {
	register(s, "review_memory", "Deterministic read-only memory health review with stable codes; never mutates storage.", func(ctx context.Context, in reviewInput) ([]app.ReviewFinding, error) {
		opts := &app.ReviewOptions{StaleTaskDays: in.StaleTaskDays, UnverifiedFactDays: in.UnverifiedFactDays, StaleRemoteDays: in.StaleRemoteDays}
		if in.StaleTaskDays == 0 && in.UnverifiedFactDays == 0 && in.StaleRemoteDays == 0 {
			opts = nil
		}
		return service.Review(ctx, cwd(in.CWD), opts)
	})
}
