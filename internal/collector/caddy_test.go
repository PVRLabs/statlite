package collector

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

type caddyTransport func(*http.Request) (*http.Response, error)

func (f caddyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func caddyTestCollector(t *testing.T, body *string, limits prometheus.Limits) *CaddyCollector {
	t.Helper()
	client, err := prometheus.NewClientWithTransport(time.Second, limits, nil, caddyTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader(*body))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return NewCaddyCollector("app", "http://localhost/metrics", "alpha.test", client)
}
func caddyPair(code, count, sum string) string {
	labels := fmt.Sprintf(`{host="alpha.test",handler="subroute",server="srv1",method="GET",code=%q}`, code)
	return "caddy_http_request_duration_seconds_count" + labels + " " + count + "\n" + "caddy_http_request_duration_seconds_sum" + labels + " " + sum + "\n"
}
func TestCaddyRecordedPopulations(t *testing.T) {
	for _, tc := range []struct {
		file, host string
		want       [5]float64
	}{
		{"https-traffic.prom", "alpha.test", [5]float64{5, 1, 2, 2, 0.002876615}},
		{"https-after-monitoring.prom", "alpha.test", [5]float64{5, 1, 2, 2, 0.002876615}},
		{"two-hosts.prom", "alpha.test", [5]float64{6, 1, 2, 2, 0.004083461}},
		{"two-hosts.prom", "beta.test", [5]float64{2, 0, 0, 0, 0.00099368}},
	} {
		t.Run(tc.file+tc.host, func(t *testing.T) {
			data, err := os.ReadFile("testdata/caddy/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			body := string(data)
			c := caddyTestCollector(t, &body, prometheus.DefaultLimits)
			c.host = tc.host
			r, err := c.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Samples) != 5 || !r.CounterContinuityBreak || r.MetricsSource != CaddyMetricsSource || r.ProcessStartTime != nil || r.HealthStatus != "" || r.DBHealthStatus != "" {
				t.Fatalf("unexpected result: %+v", r)
			}
			for i, sample := range r.Samples {
				if math.Abs(sample.Value-tc.want[i]) > 1e-12 || sample.Kind != MetricKindCounter {
					t.Fatalf("sample %d: %+v want %v", i, sample, tc.want[i])
				}
			}
			idle, err := c.Collect(context.Background())
			if err != nil || idle.CounterContinuityBreak || !reflect.DeepEqual(idle.Samples, r.Samples) {
				t.Fatalf("idle: %+v %v", idle, err)
			}
		})
	}
}
func TestCaddyRejectedPopulationsClearBaseline(t *testing.T) {
	good := caddyPair("200", "10", "1")
	cases := map[string]string{
		"initial idle": "# no traffic\n", "no fallback": "caddy_http_requests_total 20\n", "malformed suffix": good + "broken\n",
		"duplicate count": good + strings.Split(good, "\n")[0] + "\n", "duplicate sum": good + strings.Split(good, "\n")[1] + "\n",
		"missing sum": strings.Split(good, "\n")[0] + "\n", "missing count": strings.Split(good, "\n")[1] + "\n",
		"extra label mismatch": strings.Replace(good, `code="200"}`, `code="200",extra="a"}`, 1),
		"ambiguous":            good + strings.ReplaceAll(caddyPair("500", "10", "1"), "srv1", "srv2"),
		"aggregate count":      caddyPair("200", "9007199254740991", "1") + caddyPair("500", "1", "1"),
		"aggregate sum":        caddyPair("200", "1", "1e308") + caddyPair("500", "1", "1e308"),
		"invalid code":         caddyPair("600", "1", "1"),
	}
	for _, label := range []string{`host="alpha.test",`, `handler="subroute",`, `server="srv1",`, `method="GET",`, `code="200"`} {
		cases["missing "+label] = strings.ReplaceAll(good, label, "")
	}
	for _, value := range []string{"NaN", "+Inf", "-Inf", "-1", "0.5", "9007199254740992", "9007199254740993", "1e100", "oops"} {
		cases["count "+value] = caddyPair("200", value, "1")
	}
	for _, value := range []string{"NaN", "+Inf", "-Inf", "-1", "oops"} {
		cases["sum "+value] = caddyPair("200", "1", value)
	}
	var many strings.Builder
	for i := 0; i <= caddyMaxTuples; i++ {
		many.WriteString(strings.ReplaceAll(caddyPair("200", "1", "1"), `method="GET"`, fmt.Sprintf(`method="M%d"`, i)))
	}
	cases["tuple bound"] = many.String()
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			body := good
			c := caddyTestCollector(t, &body, prometheus.DefaultLimits)
			if _, err := c.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			body = bad
			r, err := c.Collect(context.Background())
			if err == nil || len(r.Samples) != 0 || len(r.Events) == 0 || c.previous != nil {
				t.Fatalf("rejected poll: %+v err=%v", r, err)
			}
			if name == "initial idle" && !strings.Contains(err.Error(), "HTTP metrics not yet observed") {
				t.Fatal(err)
			}
			if name == "ambiguous" && !strings.Contains(err.Error(), "ambiguous") {
				t.Fatal(err)
			}
			body = good
			r, err = c.Collect(context.Background())
			if err != nil || !r.CounterContinuityBreak {
				t.Fatalf("recovery: %+v %v", r, err)
			}
		})
	}
}
func TestCaddyContinuity(t *testing.T) {
	initial := caddyPair("200", "100", "10") + caddyPair("500", "100", "10")
	for _, tc := range []struct {
		name, next string
		broken     bool
	}{
		{"idle", initial, false},
		{"growth", caddyPair("200", "110", "11") + caddyPair("500", "110", "11"), false},
		{"new tuple", initial + caddyPair("404", "5", "0.5"), false},
		{"hidden count reset", caddyPair("200", "1", "10") + caddyPair("500", "300", "30"), true},
		{"sum reset", caddyPair("200", "110", "1") + caddyPair("500", "300", "30"), true},
		{"disappearance", caddyPair("500", "300", "30"), true},
		{"fingerprint", strings.ReplaceAll(caddyPair("200", "300", "30")+caddyPair("500", "300", "30"), "srv1", "srv2"), true},
		{"label order", strings.ReplaceAll(initial, `host="alpha.test",handler="subroute"`, `handler="subroute",host="alpha.test"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := initial
			c := caddyTestCollector(t, &body, prometheus.DefaultLimits)
			r, err := c.Collect(context.Background())
			if err != nil || !r.CounterContinuityBreak {
				t.Fatalf("first: %+v %v", r, err)
			}
			body = tc.next
			r, err = c.Collect(context.Background())
			if err != nil || r.CounterContinuityBreak != tc.broken {
				t.Fatalf("next: %+v %v", r, err)
			}
		})
	}
}
func TestCaddyScrapeLimitsAndTransportRecovery(t *testing.T) {
	good := caddyPair("200", "10", "1")
	for _, mode := range []string{"bytes", "samples", "labels", "label bytes", "transport"} {
		t.Run(mode, func(t *testing.T) {
			limits := prometheus.DefaultLimits
			switch mode {
			case "bytes":
				limits.MaxDecompressedBytes = int64(len(good) + 10)
			case "samples":
				limits.MaxSamples = 2
			case "labels":
				limits.MaxLabelsPerSample = 5
			case "label bytes":
				limits.MaxLabelBytes = 64
			}
			body := good
			c := caddyTestCollector(t, &body, limits)
			if _, err := c.Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "bytes":
				body += strings.Repeat("# padding\n", 100)
			case "samples":
				body += "other 1\n"
			case "labels":
				body += `other{a="1",b="2",c="3",d="4",e="5",f="6"} 1` + "\n"
			case "label bytes":
				body += `other{a="` + strings.Repeat("x", 100) + `"} 1` + "\n"
			case "transport":
				c.client, _ = prometheus.NewClientWithTransport(time.Second, limits, nil, caddyTransport(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("connection lost") }))
			}
			r, err := c.Collect(context.Background())
			if err == nil || len(r.Samples) != 0 || c.previous != nil {
				t.Fatalf("failed scrape: %+v %v", r, err)
			}
			body = good
			replacement := caddyTestCollector(t, &body, limits)
			c.client = replacement.client
			r, err = c.Collect(context.Background())
			if err != nil || !r.CounterContinuityBreak {
				t.Fatalf("recovery: %+v %v", r, err)
			}
		})
	}
}

func TestCaddyFullTuplePairingAndBoundaries(t *testing.T) {
	good := caddyPair("200", "9007199254740991", "0.25")
	// Only the count has its label order changed. An additional label must still
	// pair exactly, including escapes and delimiter-like content.
	good = strings.Replace(good, `host="alpha.test",handler="subroute"`, `handler="subroute",host="alpha.test"`, 1)
	good = strings.ReplaceAll(good, `code="200"}`, `code="200",extra="quoted\"\\value"}`)
	body := good
	c := caddyTestCollector(t, &body, prometheus.DefaultLimits)
	r, err := c.Collect(context.Background())
	if err != nil || r.Samples[0].Value != caddyMaxCount || r.Samples[4].Value != 0.25 {
		t.Fatalf("paired: %+v %v", r, err)
	}
	body += strings.Replace(strings.Split(good, "\n")[0], `handler="subroute",host="alpha.test"`, `host="alpha.test",handler="subroute"`, 1) + "\n"
	r, err = c.Collect(context.Background())
	if err == nil || len(r.Samples) != 0 {
		t.Fatalf("reordered duplicate: %+v %v", r, err)
	}
	var bounded strings.Builder
	for i := 0; i < caddyMaxTuples; i++ {
		bounded.WriteString(strings.ReplaceAll(caddyPair("200", "0", "0"), `method="GET"`, fmt.Sprintf(`method="M%d"`, i)))
	}
	body = bounded.String()
	r, err = c.Collect(context.Background())
	if err != nil || len(c.previous) != caddyMaxTuples || r.Samples[0].Value != 0 || !r.CounterContinuityBreak {
		t.Fatalf("exact bound: %+v %v", r, err)
	}
}

type caddyBrokenReader struct{}

func (caddyBrokenReader) Read([]byte) (int, error) { return 0, fmt.Errorf("response interrupted") }
func TestCaddyInterruptedResponseDiscardsValidPrefix(t *testing.T) {
	body := caddyPair("200", "10", "1")
	c := caddyTestCollector(t, &body, prometheus.DefaultLimits)
	if _, err := c.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := c.client
	var err error
	c.client, err = prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, caddyTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader(body), caddyBrokenReader{}))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Collect(context.Background())
	if err == nil || len(r.Samples) != 0 || c.previous != nil {
		t.Fatalf("interrupted: %+v %v", r, err)
	}
	c.client = original
	r, err = c.Collect(context.Background())
	if err != nil || !r.CounterContinuityBreak {
		t.Fatalf("recovery: %+v %v", r, err)
	}
}
