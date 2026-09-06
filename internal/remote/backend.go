// Package remote adapts registered transports without exposing storage to them.
package remote

import (
	"context"
	"encoding/json"
	"fmt"

	"elephant/internal/model"
	"elephant/internal/remote/ash"
)

type Backend interface {
	Exchange(context.Context, model.Remote, []byte) ([]byte, error)
}
type ASH struct{ ash.Backend }

func Config(raw string) (ash.Config, error) {
	var c ash.Config
	err := json.Unmarshal([]byte(raw), &c)
	if err != nil {
		return c, err
	}
	return c, ash.ValidateConfig(c)
}
func Validate(backend, config string) error {
	if backend != "ash" {
		return fmt.Errorf("%w: unsupported backend %q", model.ErrInvalidInput, backend)
	}
	_, err := Config(config)
	if err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalidInput, err)
	}
	return nil
}
func (b ASH) Exchange(ctx context.Context, r model.Remote, payload []byte) ([]byte, error) {
	if err := Validate(r.Backend, r.BackendConfig); err != nil {
		return nil, err
	}
	c, _ := Config(r.BackendConfig)
	return b.Backend.Exchange(ctx, c, payload)
}
