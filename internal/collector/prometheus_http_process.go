package collector

import (
	"math"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const maxPrometheusHTTPTuples = 256

// DirectRoutePrometheusLimits requires the actual scrape route so its handler
// can be excluded from the request and duration populations.
func DirectRoutePrometheusLimits() prometheus.Limits {
	limits := prometheus.DefaultLimits
	limits.MaxRedirects = 0
	return limits
}

// prometheusHTTPStatus returns an exact code or a class representative (e.g. 400).
func prometheusHTTPStatus(label string) (code int, grouped, valid bool) {
	if len(label) != 3 || label[0] < '1' || label[0] > '5' {
		return 0, false, false
	}
	code = int(label[0]-'0') * 100
	if label[1:] == "xx" {
		return code, true, true
	}
	if label[1] < '0' || label[1] > '9' || label[2] < '0' || label[2] > '9' {
		return 0, false, false
	}
	return code + int(label[1]-'0')*10 + int(label[2]-'0'), false, true
}

type prometheusRequestTotals struct {
	requests, notFound, clientErrors, serverErrors float64
	grouped                                        bool
}

func (t *prometheusRequestTotals) add(count float64, code int, grouped bool) bool {
	if count > maxExactPrometheusCount-t.requests {
		return false
	}
	t.requests += count
	t.grouped = t.grouped || grouped
	switch {
	case code >= 500:
		t.serverErrors += count
	case code >= 400:
		t.clientErrors += count
		if code == 404 {
			t.notFound += count
		}
	}
	return true
}

func (t prometheusRequestTotals) emit(result *CollectionResult) {
	result.addSample("http_requests_total", MetricKindCounter, t.requests, "requests")
	result.addSample("http_4xx_total", MetricKindCounter, t.clientErrors, "requests")
	result.addSample("http_5xx_total", MetricKindCounter, t.serverErrors, "requests")
	if !t.grouped {
		result.addSample("http_404_total", MetricKindCounter, t.notFound, "requests")
	}
}

type prometheusUnlabelledValue struct {
	value       float64
	seen, valid bool
}

func (m *prometheusUnlabelledValue) accept(sample prometheus.Sample) {
	m.valid = !m.seen && len(sample.Labels) == 0 && finiteNonnegative(sample.Value)
	m.seen = true
	m.value = sample.Value
}

type prometheusProcess struct {
	start, cpu     prometheusUnlabelledValue
	startTimeSlack time.Duration
}

func (p *prometheusProcess) accept(sample prometheus.Sample) bool {
	switch sample.Name {
	case "process_start_time_seconds":
		p.start.accept(sample)
	case "process_cpu_seconds_total":
		p.cpu.accept(sample)
	default:
		return false
	}
	return true
}

func (p prometheusProcess) startTime(at time.Time) *time.Time {
	if !p.start.valid || p.start.value <= 0 || p.start.value >= 1<<53 {
		return nil
	}
	seconds, fraction := math.Modf(p.start.value)
	start := time.Unix(int64(seconds), int64(math.Round(fraction*1e9))).UTC()
	if start.After(at.Add(p.startTimeSlack)) {
		return nil
	}
	return &start
}

type prometheusProcessBaseline struct {
	at       time.Time
	identity float64
	cpu      prometheusUnlabelledValue
}

func (p prometheusProcess) emit(result *CollectionResult, previous prometheusProcessBaseline, at time.Time) prometheusProcessBaseline {
	current := prometheusProcessBaseline{at: at, cpu: p.cpu}
	result.ProcessStartTime = p.startTime(at)
	if result.ProcessStartTime == nil {
		return current
	}
	current.identity = p.start.value
	result.addSample("process_uptime", MetricKindGauge, max(0, at.Sub(*result.ProcessStartTime).Seconds()), "seconds")
	elapsed := at.Sub(previous.at).Seconds()
	if current.identity == previous.identity && p.cpu.valid && previous.cpu.valid &&
		p.cpu.value >= previous.cpu.value && elapsed > 0 {
		cores := (p.cpu.value - previous.cpu.value) / elapsed
		if finiteNonnegative(cores) {
			result.addSample("process_cpu_usage", MetricKindGauge, cores, "cores")
		}
	}
	return current
}
