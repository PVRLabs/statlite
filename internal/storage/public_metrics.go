package storage

import "time"

// PublicMetricPoint contains only the derived values needed by the public API.
// A timestamp is the start of the UTC minute containing its source polls.
type PublicMetricPoint struct {
	Timestamp             time.Time
	Requests              *float64
	HTTP4xx               *float64
	HTTP5xx               *float64
	HTTP5xxRate           *float64
	AverageLatencySeconds *float64
	LatencyRequests       *float64
	ProcessCPUCores       *float64
	RuntimeMemoryBytes    *float64
	HostCPUUsage          *float64
	HostMemoryUsage       *float64
	HostDiskUsage         *float64
}

type PublicMetricSeries struct {
	Points                  []PublicMetricPoint
	LatestHTTPObservationAt *time.Time
}

// GroupPublicMetrics assigns each complete poll delta to its poll's UTC minute.
// Long intervals and collection-gap recovery deltas remain whole in their end
// slot; a slot does not claim that all counted traffic occurred in that minute.
func GroupPublicMetrics(series *Series) PublicMetricSeries {
	out := PublicMetricSeries{Points: []PublicMetricPoint{}}
	if series == nil {
		return out
	}
	var acc *bucketAccumulator
	var requestIntervals, paired4xx, paired5xx int
	var sum4xx, sum5xx float64
	flush := func() {
		if acc == nil {
			return
		}
		point := acc.point()
		public := PublicMetricPoint{
			Timestamp:             acc.start,
			Requests:              point.Requests,
			AverageLatencySeconds: point.AverageLatencySeconds,
			ProcessCPUCores:       point.ProcessCPUUsage,
			RuntimeMemoryBytes:    point.HeapUsedBytes,
			HostCPUUsage:          point.HostCPUUsage,
			HostMemoryUsage:       point.HostMemoryUsage,
			HostDiskUsage:         point.HostDiskUsage,
		}
		if acc.latencyRequestsSum > 0 {
			value := acc.latencyRequestsSum
			public.LatencyRequests = &value
		}
		if requestIntervals > 0 && paired4xx == requestIntervals {
			value := sum4xx
			public.HTTP4xx = &value
		}
		if requestIntervals > 0 && paired5xx == requestIntervals {
			value := sum5xx
			public.HTTP5xx = &value
			if public.Requests != nil && *public.Requests > 0 {
				rate := value / *public.Requests
				public.HTTP5xxRate = &rate
			}
		}
		out.Points = append(out.Points, public)
	}
	for _, point := range series.Points {
		paired4 := point.Requests != nil && point.publicCoverage.paired4xx && point.HTTP4xx != nil
		paired5 := point.Requests != nil && point.publicCoverage.paired5xx && point.HTTP5xx != nil
		usable := point.Requests != nil || point.AverageLatencySeconds != nil ||
			point.HeapUsedBytes != nil || point.ProcessCPUUsage != nil || point.HostCPUUsage != nil ||
			point.HostMemoryUsage != nil || point.HostDiskUsage != nil
		if !usable {
			continue
		}
		if point.Requests != nil || point.AverageLatencySeconds != nil || paired4 || paired5 {
			timestamp := point.Timestamp.UTC()
			out.LatestHTTPObservationAt = &timestamp
		}
		slot := point.Timestamp.UTC().Truncate(time.Minute)
		if acc == nil || !acc.start.Equal(slot) {
			flush()
			acc = newBucketAccumulator(slot)
			requestIntervals, paired4xx, paired5xx = 0, 0, 0
			sum4xx, sum5xx = 0, 0
		}
		acc.add(point)
		if point.Requests != nil {
			requestIntervals++
			if paired4 {
				paired4xx++
				sum4xx += *point.HTTP4xx
			}
			if paired5 {
				paired5xx++
				sum5xx += *point.HTTP5xx
			}
		}
	}
	flush()
	return out
}
