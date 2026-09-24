package server

import (
	"context"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

// readPublicMetrics uses request time for the rolling hour. The caller selects
// an exact configured target before invoking this read-only path.
func (s *Server) readPublicMetrics(ctx context.Context, target monitor.ManagedTarget, evaluatedAt time.Time) (storage.PublicMetricSeries, error) {
	end := evaluatedAt.UTC()
	start, cutoff, clamped := s.clampToRetention(end.Add(-time.Hour))
	if !start.Before(end) {
		return storage.PublicMetricSeries{Points: []storage.PublicMetricPoint{}}, nil
	}
	series, err := target.Monitor.BoundedSeries(ctx, start, end, cutoff)
	if err != nil {
		return storage.PublicMetricSeries{}, err
	}
	if clamped {
		clearCutoffCounterBaseline(series, cutoff)
	}
	return storage.GroupPublicMetrics(series), nil
}
