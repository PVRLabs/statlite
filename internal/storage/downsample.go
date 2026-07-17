package storage

// This file aggregates series points into fixed time buckets for dashboard charts.

import "time"

// AggregateSeries groups points into buckets of bucketDuration aligned to stable
// UTC clock boundaries (time.Truncate). That keeps rolling dashboard ranges
// (1h/7d/30d) from shifting bucket edges on every refresh. The first partial
// bucket uses series.Start so its timestamp never precedes the requested range.
//
// Counter-like fields are summed; gauges and latency are averaged. Null metrics
// stay null. Empty buckets are not synthesized. Bucket identities are retained
// only when every contributing sample has the same value.
//
// Aggregation runs in a single pass. If no two points share a bucket, the
// original series is returned unchanged (native resolution). Aggregation
// assumes points already contain restart-aware counter deltas.
func AggregateSeries(series *Series, bucketDuration time.Duration) *Series {
	if series == nil || bucketDuration <= 0 || len(series.Points) < 2 {
		return series
	}

	out := &Series{
		Start:  series.Start,
		End:    series.End,
		Points: make([]SeriesPoint, 0, len(series.Points)),
	}

	merged := false
	var acc *bucketAccumulator
	flush := func() {
		if acc == nil {
			return
		}
		out.Points = append(out.Points, acc.point())
		acc = nil
	}

	for i := range series.Points {
		point := series.Points[i]
		bucketStart := clockBucketStart(point.Timestamp, bucketDuration)
		if bucketStart.Before(series.Start) {
			// Keep the first partial bucket inside the requested range while
			// leaving every later bucket on a stable UTC clock boundary.
			bucketStart = series.Start.UTC()
		}
		if acc == nil || !acc.start.Equal(bucketStart) {
			flush()
			acc = newBucketAccumulator(bucketStart)
			acc.add(point)
			continue
		}
		merged = true
		acc.add(point)
	}
	flush()

	if !merged {
		// Sparse series: at most one sample per clock bucket — keep native points.
		return series
	}
	return out
}

// clockBucketStart returns the UTC start of the bucket containing timestamp,
// aligned to absolute clock multiples of bucketDur (minute, 5m, 30m, 2h, …).
func clockBucketStart(timestamp time.Time, bucketDur time.Duration) time.Time {
	if bucketDur <= 0 {
		return timestamp.UTC()
	}
	return timestamp.UTC().Truncate(bucketDur)
}

type bucketAccumulator struct {
	start time.Time
	// Identity is retained only while every sample in this bucket agrees.
	identitySet  bool
	pollIDCommon bool
	appRunCommon bool
	pollID       int64
	appRunID     *int64

	requestsSum, http404Sum, http4xxSum, http5xxSum float64
	requestsN, http404N, http4xxN, http5xxN         int

	latencySum, heapSum, cpuSum float64
	latencyN, heapN, cpuN       int
}

func newBucketAccumulator(start time.Time) *bucketAccumulator {
	return &bucketAccumulator{start: start}
}

func (a *bucketAccumulator) add(point SeriesPoint) {
	if !a.identitySet {
		a.identitySet = true
		a.pollIDCommon = true
		a.appRunCommon = true
		a.pollID = point.PollID
		a.appRunID = point.AppRunID
	} else {
		if a.pollIDCommon && a.pollID != point.PollID {
			a.pollIDCommon = false
			a.pollID = 0
		}
		if a.appRunCommon && !sameAppRun(a.appRunID, point.AppRunID) {
			a.appRunCommon = false
			a.appRunID = nil
		}
	}

	addSum(&a.requestsSum, &a.requestsN, point.Requests)
	addSum(&a.http404Sum, &a.http404N, point.HTTP404)
	addSum(&a.http4xxSum, &a.http4xxN, point.HTTP4xx)
	addSum(&a.http5xxSum, &a.http5xxN, point.HTTP5xx)
	addSum(&a.latencySum, &a.latencyN, point.AverageLatencySeconds)
	addSum(&a.heapSum, &a.heapN, point.HeapUsedBytes)
	addSum(&a.cpuSum, &a.cpuN, point.ProcessCPUUsage)
}

func (a *bucketAccumulator) point() SeriesPoint {
	point := SeriesPoint{
		Timestamp:             a.start,
		Requests:              sumResult(a.requestsSum, a.requestsN),
		HTTP404:               sumResult(a.http404Sum, a.http404N),
		HTTP4xx:               sumResult(a.http4xxSum, a.http4xxN),
		HTTP5xx:               sumResult(a.http5xxSum, a.http5xxN),
		AverageLatencySeconds: avgResult(a.latencySum, a.latencyN),
		HeapUsedBytes:         avgResult(a.heapSum, a.heapN),
		ProcessCPUUsage:       avgResult(a.cpuSum, a.cpuN),
	}
	if a.pollIDCommon {
		point.PollID = a.pollID
	}
	if a.appRunCommon {
		point.AppRunID = a.appRunID
	}
	return point
}

func addSum(sum *float64, n *int, value *float64) {
	if value == nil {
		return
	}
	*sum += *value
	*n++
}

func sumResult(sum float64, n int) *float64 {
	if n == 0 {
		return nil
	}
	v := sum
	return &v
}

func avgResult(sum float64, n int) *float64 {
	if n == 0 {
		return nil
	}
	v := sum / float64(n)
	return &v
}
