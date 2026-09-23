package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TargetIdentity is the stable integration type assigned to a configured name.
type TargetIdentity struct {
	Name string
	Type string
}

// RegisterTargets binds all configured names atomically. Existing history is
// retained, including when a name is absent from the current configuration.
func (s *Store) RegisterTargets(ctx context.Context, targets []TargetIdentity) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin target registration: %w", err)
	}
	defer tx.Rollback()

	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.Name) == "" || strings.TrimSpace(target.Type) == "" {
			return fmt.Errorf("target registration requires a name and canonical type")
		}
		if seen[target.Name] {
			return fmt.Errorf("target %q occurs more than once in registration", target.Name)
		}
		seen[target.Name] = true

		var existing sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT target_type FROM targets WHERE name = ?`, target.Name).Scan(&existing)
		switch err {
		case nil:
			if existing.Valid && existing.String != target.Type {
				return fmt.Errorf("target %q was registered as type %q, but is configured as type %q; restore the previous type, use a new target name, or start with a new database", target.Name, existing.String, target.Type)
			}
			if !existing.Valid {
				if _, err := tx.ExecContext(ctx, `UPDATE targets SET target_type = ? WHERE name = ?`, target.Type, target.Name); err != nil {
					return fmt.Errorf("bind target %q: %w", target.Name, err)
				}
			}
		case sql.ErrNoRows:
			if _, err := tx.ExecContext(ctx, `INSERT INTO targets (name, created_at, target_type) VALUES (?, ?, ?)`, target.Name, formatSortableTime(time.Now().UTC()), target.Type); err != nil {
				return fmt.Errorf("register target %q: %w", target.Name, err)
			}
		default:
			return fmt.Errorf("read target %q identity: %w", target.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit target registration: %w", err)
	}
	return nil
}

func registeredTarget(ctx context.Context, tx *sql.Tx, name string) (int64, string, error) {
	var id int64
	var targetType sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT id, target_type FROM targets WHERE name = ?`, name).Scan(&id, &targetType); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", fmt.Errorf("target %q is not registered", name)
		}
		return 0, "", fmt.Errorf("query registered target %q: %w", name, err)
	}
	if !targetType.Valid {
		return 0, "", fmt.Errorf("target %q has no registered type", name)
	}
	return id, targetType.String, nil
}
