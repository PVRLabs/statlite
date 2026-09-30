// Bounded normalization shared by explicit Micrometer framework adapters.
package collector

import (
	"hash/fnv"
	"math/bits"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// micrometerHTTPLabelValidator returns the framework-validated numeric status.
// Framework adapters own required labels and accepted status syntax.
type micrometerHTTPLabelValidator func(prometheus.Sample) (int, bool)

// micrometerHTTPValues retains fixed-size StatLite aggregates plus a bounded set
// of label fingerprints used only to match timer count and sum populations.
// Source labels and series are inspected while streaming and are not retained.
type micrometerHTTPValues struct {
	requests             float64
	durationSeconds      float64
	notFound             float64
	clientErrors         float64
	serverErrors         float64
	sawCount             bool
	sawDuration          bool
	incompleteCountLabel bool
	incompleteSumLabel   bool
	countDimensions      map[micrometerHTTPDimensionHash]int
	durationDimensions   map[micrometerHTTPDimensionHash]int
	matchingStates       int
	matchingOverflow     bool
	requestOverflow      bool
	notFoundOverflow     bool
	clientErrorsOverflow bool
	serverErrorsOverflow bool
	durationOverflow     bool
	countDuplicate       bool
	durationDuplicate    bool
}

type micrometerHTTPDimensionHash struct{ first, second uint64 }

type micrometerRuntimeValues struct {
	cpu, heap, processStart, uptime                             float64
	sawCPU, sawHeap, sawProcessStart, sawUptime                 bool
	invalidCPU, invalidHeap, invalidProcessStart, invalidUptime bool
}

// Count and sum share a fixed budget of transient identities, independent of
// the parser's sample limit. Each population consumes this combined budget.
const micrometerHTTPMatchingStateLimit = 20_000

func (v *micrometerRuntimeValues) acceptCPU(value float64) bool {
	if v.sawCPU || !finiteInRange(value, 0, 1) {
		v.invalidCPU = true
		return false
	}
	v.sawCPU, v.cpu = true, value
	return true
}

func (v *micrometerRuntimeValues) acceptHeap(value float64) bool {
	if !finiteNonnegative(value) {
		v.invalidHeap = true
		return false
	}
	var ok bool
	v.heap, ok = addFiniteNonnegative(v.heap, value)
	v.sawHeap = true
	v.invalidHeap = v.invalidHeap || !ok
	return ok
}

func (v *micrometerRuntimeValues) acceptProcessStart(value float64) bool {
	if v.sawProcessStart || !finiteNonnegative(value) || value == 0 || !rfc3339RoundTripsUnixSeconds(value) {
		v.invalidProcessStart = true
		return false
	}
	v.sawProcessStart, v.processStart = true, value
	return true
}

func rfc3339RoundTripsUnixSeconds(value float64) bool {
	converted := unixSeconds(value)
	formatted := converted.Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, formatted)
	return err == nil && parsed.Equal(converted)
}

func (v *micrometerRuntimeValues) acceptUptime(value float64) {
	if v.sawUptime || !finiteNonnegative(value) {
		v.invalidUptime = true
		return
	}
	v.sawUptime, v.uptime = true, value
}

func (v micrometerRuntimeValues) addTo(result *CollectionResult) {
	if v.sawCPU && !v.invalidCPU {
		result.addSample("process_cpu_usage", MetricKindGauge, v.cpu, "ratio")
	}
	if v.sawHeap && !v.invalidHeap {
		result.addSample("jvm_heap_used_bytes", MetricKindGauge, v.heap, "bytes")
	}
	if v.sawProcessStart && !v.invalidProcessStart {
		result.addSample("process_start_time", MetricKindGauge, v.processStart, "unix_seconds")
		started := unixSeconds(v.processStart)
		result.ProcessStartTime = &started
	}
	if v.sawUptime && !v.invalidUptime {
		result.addSample("process_uptime", MetricKindGauge, v.uptime, "seconds")
	}
}

func (v *micrometerHTTPValues) acceptCount(sample prometheus.Sample, validate micrometerHTTPLabelValidator) {
	code, ok := validate(sample)
	if !ok || !finiteNonnegative(sample.Value) {
		v.incompleteCountLabel = true
		return
	}
	dimension := micrometerHTTPSeriesFingerprint(sample.Labels)
	if v.countDimensions[dimension] > 0 {
		v.countDuplicate = true
		return
	}
	v.sawCount = true
	var added bool
	v.requests, added = addFiniteNonnegative(v.requests, sample.Value)
	if !added {
		v.requestOverflow = true
	}
	v.addMatchingDimension(&v.countDimensions, dimension)
	if code == 404 {
		v.notFound, added = addFiniteNonnegative(v.notFound, sample.Value)
		v.notFoundOverflow = v.notFoundOverflow || !added
	}
	if code >= 400 && code <= 499 {
		v.clientErrors, added = addFiniteNonnegative(v.clientErrors, sample.Value)
		v.clientErrorsOverflow = v.clientErrorsOverflow || !added
	}
	if code >= 500 {
		v.serverErrors, added = addFiniteNonnegative(v.serverErrors, sample.Value)
		v.serverErrorsOverflow = v.serverErrorsOverflow || !added
	}
}

func (v *micrometerHTTPValues) acceptDuration(sample prometheus.Sample, validate micrometerHTTPLabelValidator) {
	_, ok := validate(sample)
	if !ok || !finiteNonnegative(sample.Value) {
		v.incompleteSumLabel = true
		return
	}
	dimension := micrometerHTTPSeriesFingerprint(sample.Labels)
	if v.durationDimensions[dimension] > 0 {
		v.durationDuplicate = true
		return
	}
	v.sawDuration = true
	var added bool
	v.durationSeconds, added = addFiniteNonnegative(v.durationSeconds, sample.Value)
	if !added {
		v.durationOverflow = true
	}
	v.addMatchingDimension(&v.durationDimensions, dimension)
}

func (v *micrometerHTTPValues) addMatchingDimension(groups *map[micrometerHTTPDimensionHash]int, key micrometerHTTPDimensionHash) {
	if v.matchingOverflow {
		return
	}
	if *groups == nil {
		*groups = make(map[micrometerHTTPDimensionHash]int)
	}
	if _, exists := (*groups)[key]; !exists {
		if v.matchingStates == micrometerHTTPMatchingStateLimit {
			v.matchingOverflow = true
			return
		}
		v.matchingStates++
	}
	(*groups)[key]++
}

func micrometerHTTPSeriesFingerprint(labels []prometheus.Label) micrometerHTTPDimensionHash {
	// Label order is not part of Prometheus series identity. Combine hashes of
	// every bounded name/value pair commutatively so count and sum still match
	// when exposition order differs, without retaining any raw label.
	var fingerprint micrometerHTTPDimensionHash
	for _, label := range labels {
		first := hashHTTPDimensions(fnv.New64a(), label.Name, label.Value)
		second := hashHTTPDimensions(fnv.New64(), label.Name, label.Value)
		fingerprint.first += first
		fingerprint.second += second ^ bits.RotateLeft64(first, 23)
	}
	fingerprint.first ^= uint64(len(labels)) * 0x9e3779b97f4a7c15
	fingerprint.second ^= uint64(len(labels)) * 0xc2b2ae3d27d4eb4f
	return fingerprint
}

func hashHTTPDimensions(hash interface {
	Write([]byte) (int, error)
	Sum64() uint64
}, values ...string) uint64 {
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return hash.Sum64()
}

func (v *micrometerHTTPValues) durationMatchesCounts() bool {
	if v.matchingOverflow || v.durationOverflow || v.countDuplicate || v.durationDuplicate || !v.sawCount || !v.sawDuration || len(v.countDimensions) != len(v.durationDimensions) {
		return false
	}
	for key, countSeries := range v.countDimensions {
		if v.durationDimensions[key] != countSeries {
			return false
		}
	}
	return true
}

func (v *micrometerHTTPValues) addTo(result *CollectionResult) {
	if v.sawCount && !v.requestOverflow && !v.countDuplicate && !v.matchingOverflow {
		result.addSample("http_requests_total", MetricKindCounter, v.requests, "requests")
	}
	if v.sawCount && !v.incompleteCountLabel && !v.countDuplicate && !v.matchingOverflow {
		if !v.notFoundOverflow {
			result.addSample("http_404_total", MetricKindCounter, v.notFound, "requests")
		}
		if !v.clientErrorsOverflow {
			result.addSample("http_4xx_total", MetricKindCounter, v.clientErrors, "requests")
		}
		if !v.serverErrorsOverflow {
			result.addSample("http_5xx_total", MetricKindCounter, v.serverErrors, "requests")
		}
	}
	durationMatches := v.durationMatchesCounts()
	if durationMatches {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, v.durationSeconds, "seconds")
	}
}

func (v *micrometerRuntimeValues) accept(sample prometheus.Sample) {
	switch sample.Name {
	case "process_cpu_usage":
		v.acceptCPU(sample.Value)
	case "process_start_time_seconds":
		v.acceptProcessStart(sample.Value)
	case "process_uptime_seconds":
		v.acceptUptime(sample.Value)
	case "jvm_memory_used_bytes":
		for _, label := range sample.Labels {
			if label.Name == "area" && label.Value == "heap" {
				v.acceptHeap(sample.Value)
				break
			}
		}
	}
}
