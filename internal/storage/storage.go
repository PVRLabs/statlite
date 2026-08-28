package storage

// This file owns SQLite store construction, health checks, and schema initialization.

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

const currentSchemaVersion = 1

type Store struct {
	db     *sql.DB
	closed atomic.Bool
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closed.Store(true)
	return s.db.Close()
}

// Available reports local store availability without querying or waiting on SQLite.
// It confirms only that the store initialized successfully and has not been closed.
func (s *Store) Available() bool {
	return s != nil && s.db != nil && !s.closed.Load()
}

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite store is not open")
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}
	return nil
}

func (s *Store) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite schema initialization: %w", err)
	}
	defer tx.Rollback()

	var schemaVersion int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if schemaVersion > currentSchemaVersion {
		return fmt.Errorf("sqlite schema version %d is newer than supported version %d", schemaVersion, currentSchemaVersion)
	}
	if schemaVersion != 0 && schemaVersion < currentSchemaVersion {
		return fmt.Errorf("sqlite schema version %d requires a migration to version %d", schemaVersion, currentSchemaVersion)
	}
	if schemaVersion == currentSchemaVersion {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("finish sqlite schema initialization: %w", err)
		}
		return nil
	}

	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read embedded schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, string(schema)); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", currentSchemaVersion)); err != nil {
		return fmt.Errorf("set sqlite schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish sqlite schema initialization: %w", err)
	}
	return nil
}
