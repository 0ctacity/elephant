// Package storage defines the atomic persistence boundary consumed by the app.
package storage

import (
	"context"

	"elephant/internal/model"
)

// Store serializes a complete operation in a transaction, including graph edits.
type Store interface {
	Transact(context.Context, func(Tx) error) error
}
type Tx interface {
	Project(string) (model.Project, error)
	CreateProject(model.Project) error
	Get(model.Project, string) (model.Entry, error)
	Put(model.Project, model.Entry) error
	List(model.Project, model.Filter) ([]model.Entry, error)
	Link(model.Project, model.Relation) error
	Unlink(model.Relation) error
	Relations(string) ([]model.Relation, error)
}
