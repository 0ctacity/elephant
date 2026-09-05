package zova

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	native "github.com/ata-sesli/zova/bindings/go"

	"elephant/internal/model"
	"elephant/internal/storage"
)

func TestPersistenceGraphAndRollback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "elephant.zova")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := model.Project{ID: "prj_one", Identity: "example/repo", Name: "repo", TableName: "p_0123456789abcdef0123456789abcdef", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	e := model.Entry{ID: "one", Kind: model.Fact, Title: "Known", Body: "Context", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	edge := model.Relation{From: "entry:one", Type: "concerns", To: "file:prj_one:src/main.go"}
	err = s.Transact(ctx, func(tx storage.Tx) error {
		if err := tx.CreateProject(p); err != nil {
			return err
		}
		if err := tx.Put(p, e); err != nil {
			return err
		}
		return tx.Link(p, edge)
	})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback")
	err = s.Transact(ctx, func(tx storage.Tx) error {
		e.Title = "changed"
		if err := tx.Put(p, e); err != nil {
			return err
		}
		if err := tx.Unlink(edge); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Transact(ctx, func(tx storage.Tx) error {
		got, err := tx.Project(p.Identity)
		if err != nil {
			return err
		}
		if got.ID != p.ID {
			t.Fatal(got)
		}
		entries, err := tx.List(p, model.Filter{Kind: model.Fact, Limit: 10})
		if err != nil {
			return err
		}
		if len(entries) != 1 || entries[0].Title != "Known" || entries[0].StartCommit != nil {
			t.Fatal(entries)
		}
		links, err := tx.Relations("entry:one")
		if err != nil {
			return err
		}
		if len(links) != 1 || links[0] != edge {
			t.Fatal(links)
		}
		_, err = tx.Get(p, "missing")
		if !errors.Is(err, model.ErrEntryNotFound) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.zova")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := native.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("UPDATE elephant_meta SET value='999' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err = Open(path); !errors.Is(err, model.ErrSchema) {
		if s != nil {
			s.Close()
		}
		t.Fatalf("future schema: %v", err)
	}
}

func TestOrderingAndConcurrentHandles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "order.zova")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	p := model.Project{ID: "order", Identity: "local:order", Name: "order", TableName: "p_0123456789abcdef0123456789abcdef", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	err = s.Transact(ctx, func(tx storage.Tx) error {
		if err := tx.CreateProject(p); err != nil {
			return err
		}
		for i, at := range []time.Time{base, base.Add(time.Millisecond)} {
			e := model.Entry{ID: fmt.Sprint(i), Kind: model.Fact, Title: "Fact", Body: "Context", Status: "active", CreatedAt: at, UpdatedAt: at}
			if err := tx.Put(p, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = other.Transact(ctx, func(tx storage.Tx) error {
		entries, err := tx.List(p, model.Filter{Limit: 1})
		if err != nil {
			return err
		}
		if len(entries) != 1 || entries[0].ID != "1" {
			t.Fatalf("newest first: %+v", entries)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store := s
			if i%2 == 1 {
				store = other
			}
			errs <- store.Transact(ctx, func(tx storage.Tx) error {
				return tx.Put(p, model.Entry{ID: fmt.Sprint(i + 2), Kind: model.Task, Title: "Concurrent", Body: "Context", Status: "open", CreatedAt: base, UpdatedAt: base})
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
