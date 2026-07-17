package server

// This file defines standard dashboard range identifiers and chart bucket scales.

import "time"

// DashboardRange identifies a standard dashboard time view.
type DashboardRange string

const (
	DashboardRange1H     DashboardRange = "1h"
	DashboardRangeToday  DashboardRange = "today"
	DashboardRange7D     DashboardRange = "7d"
	DashboardRange30D    DashboardRange = "30d"
	DashboardRangeCustom DashboardRange = "custom"
)

// dashboardBucketDuration returns the fixed aggregation interval for a standard
// dashboard view. The 1h view and custom start/end queries return 0 so their
// native sample resolution is preserved.
func dashboardBucketDuration(r DashboardRange) time.Duration {
	switch r {
	case DashboardRangeToday:
		return 5 * time.Minute
	case DashboardRange7D:
		return 30 * time.Minute
	case DashboardRange30D:
		return 2 * time.Hour
	default:
		return 0
	}
}
