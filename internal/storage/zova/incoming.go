package zova

import (
	"fmt"

	native "github.com/ata-sesli/zova/bindings/go"

	"elephant/internal/model"
)

// Incoming lists edges pointing at a node, capped to keep review bounded.
func (t *transaction) Incoming(id string) ([]model.Relation, error) {
	ns, err := t.db.GraphNeighbors(native.GraphNeighborsOptions{GraphName: graph, NodeID: id, Direction: native.GraphNeighborIncoming, Limit: 101})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", model.ErrStorage, err)
	}
	out := make([]model.Relation, 0, len(ns))
	for _, n := range ns {
		out = append(out, model.Relation{From: n.NodeID, Type: n.EdgeType, To: id})
	}
	return out, nil
}
