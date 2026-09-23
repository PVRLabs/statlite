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

const currentSchemaVersion = 2

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

	// Run migrations sequentially, with one committed transaction per version.
	// A failed later migration leaves the last successfully committed version.
	// Keep each transition in its own migration file.
	for {
		var schemaVersion int
		if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion); err != nil {
			return fmt.Errorf("read sqlite schema version: %w", err)
		}
		if schemaVersion > currentSchemaVersion {
			return fmt.Errorf("sqlite schema version %d is newer than supported version %d", schemaVersion, currentSchemaVersion)
		}
		switch schemaVersion {
		case 0:
			var objectCount int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&objectCount); err != nil {
				return fmt.Errorf("inspect unversioned sqlite database: %w", err)
			}
			if objectCount == 0 {
				if err := initializeFreshSchema(ctx, s.db); err != nil {
					return err
				}
			} else if err := migrateV1ToV2(ctx, s.db); err != nil {
				return err
			}
		case 1:
			if err := migrateV1ToV2(ctx, s.db); err != nil {
				return err
			}
		case 2:
			return checkV2Columns(ctx, s.db)
		default:
			return fmt.Errorf("unsupported sqlite schema version %d", schemaVersion)
		}
	}
}

func initializeFreshSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite schema initialization: %w", err)
	}
	defer tx.Rollback()

	var schemaVersion, objectCount int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`).Scan(&objectCount); err != nil {
		return fmt.Errorf("inspect unversioned sqlite database: %w", err)
	}
	if schemaVersion != 0 || objectCount != 0 {
		return fmt.Errorf("sqlite database changed during fresh schema initialization")
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
