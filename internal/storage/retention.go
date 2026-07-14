package storage

// This file deletes expired poll history and tracks the active retention cutoff.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const retentionCleanupInterval = 24 * time.Hour
const retentionCleanupTimeout = 30 * time.Second

type retentionCutoffTracker struct {
	mu     sync.RWMutex
	cutoff time.Time
}

func NewRetentionCutoffTracker(retentionDays int) *retentionCutoffTracker {
	tracker := &retentionCutoffTracker{}
	if retentionDays > 0 {
		tracker.Set(time.Now().UTC().AddDate(0, 0, -retentionDays))
	}
	return tracker
}

func (t *retentionCutoffTracker) Set(cutoff time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cutoff = cutoff.UTC()
}

func (t *retentionCutoffTracker) Current() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cutoff
}

func StartRetentionCleanup(ctx context.Context, store *Store, retentionDays int, setCutoff func(time.Time)) {
	if retentionDays == 0 {
		log.Printf("retention cleanup disabled")
		return
	}
	cleanupRetention(ctx, store, retentionDays, time.Now, setCutoff)
	go runRetentionCleanupLoop(ctx, store, retentionDays, time.Now, retentionCleanupInterval, setCutoff)
}

func runRetentionCleanupLoop(ctx context.Context, store *Store, retentionDays int, now func() time.Time, interval time.Duration, setCutoff func(time.Time)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupRetention(ctx, store, retentionDays, now, setCutoff)
		}
	}
}

func cleanupRetention(ctx context.Context, store *Store, retentionDays int, now func() time.Time, setCutoff func(time.Time)) {
	cutoff := now().UTC().AddDate(0, 0, -retentionDays)
	cleanupCtx, cancel := context.WithTimeout(ctx, retentionCleanupTimeout)
	defer cancel()

	deleted, err := store.DeletePollsBefore(cleanupCtx, cutoff)
	if err != nil {
		log.Printf("retention cleanup failed: %v", err)
		return
	}
	if setCutoff != nil {
		setCutoff(cutoff)
	}
	if deleted > 0 {
		log.Printf("retention cleanup deleted %d poll(s) before %s", deleted, cutoff.Format(time.RFC3339))
	}
}

func (s *Store) DeletePollsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("sqlite store is not open")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin retention cleanup transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
DELETE FROM polls
WHERE started_at < ?
`, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("delete expired polls: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read deleted poll count: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit retention cleanup: %w", err)
	}
	return deleted, nil
}
