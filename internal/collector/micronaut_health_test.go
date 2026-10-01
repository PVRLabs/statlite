package collector

import (
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestParseMicronautHealthResponse(t *testing.T) {
	tests := []struct {
		name, body, app, db string
		partial, invalid    bool
	}{
		{"up", `{"status":" up "}`, "UP", "", false, false},
		{"down", `{"status":"DOWN","details":{"jdbc":{"status":"down"}}}`, "DOWN", "DOWN", false, false},
		{"hidden", `{"status":"UP","details":null}`, "UP", "", false, false},
		{"absent jdbc", `{"status":"UP","details":{"diskSpace":17}}`, "UP", "", false, false},
		{"null jdbc", `{"status":"UP","details":{"jdbc":null}}`, "UP", "", false, false},
		{"independent aggregate", `{"status":"DOWN","details":{"jdbc":{"status":"UP","details":{"one":{"status":"DOWN"}}}}}`, "DOWN", "UP", false, false},
		{"ignored children", `{"status":"UP","details":{"jdbc":{"status":"DOWN","details":[17,null,"anything"]}}}`, "UP", "DOWN", false, false},
		{"invalid details", `{"status":"UP","details":[]}`, "UP", "", true, false},
		{"invalid jdbc", `{"status":"UP","details":{"jdbc":"UP"}}`, "UP", "", true, false},
		{"empty jdbc", `{"status":"UP","details":{"jdbc":{}}}`, "UP", "", true, false},
		{"unknown jdbc", `{"status":"UP","details":{"jdbc":{"status":"UNKNOWN"}}}`, "UP", "", true, false},
		{"numeric jdbc", `{"status":"UP","details":{"jdbc":{"status":1}}}`, "UP", "", true, false},
		{"unknown app", `{"status":"UNKNOWN","details":{"jdbc":{"status":"UP"}}}`, "", "", false, true},
		{"custom app", `{"status":"DEGRADED"}`, "", "", false, true},
		{"missing app", `{"details":{"jdbc":{"status":"UP"}}}`, "", "", false, true},
		{"null app", `{"status":null}`, "", "", false, true},
		{"numeric app", `{"status":1}`, "", "", false, true},
		{"empty app", `{"status":""}`, "", "", false, true},
		{"array root", `[]`, "", "", false, true},
		{"null root", `null`, "", "", false, true},
		{"trailing JSON", `{"status":"UP"}{}`, "", "", false, true},
		{"malformed", `{"status":"UP"`, "", "", false, true},
		{"invalid ignored detail JSON", `{"status":"UP","details":{"other":invalid}}`, "", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := ParseMicronautHealthResponse([]byte(tt.body))
			if tt.invalid {
				if err == nil || h != nil {
					t.Fatalf("got %#v, %v", h, err)
				}
				return
			}
			if err != nil || h.Status != tt.app || h.DatabaseStatus != tt.db || (h.Warning != "") != tt.partial {
				t.Fatalf("got %#v, %v", h, err)
			}
		})
	}
}

func TestMicronautHealthTransport(t *testing.T) {
	for _, code := range []int{200, 503, 401, 403, 404, 410, 500} {
		for _, status := range []string{"UP", "DOWN"} {
			t.Run(fmt.Sprintf("%d/%s", code, status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					u, p, ok := r.BasicAuth()
					if !ok || u != "user" || p != "secret" {
						t.Error("missing auth")
					}
					if r.RequestURI != "/health/?probe=1" {
						t.Errorf("URL changed: %s", r.RequestURI)
					}
					w.WriteHeader(code)
					fmt.Fprintf(w, `{"status":%q}`, status)
				}))
				defer server.Close()
				c, err := NewMicronautHealthClient(server.URL+"/health/?probe=1", time.Second, &BasicAuth{Username: "user", Password: "secret"})
				if err != nil {
					t.Fatal(err)
				}
				h, err := c.Fetch(context.Background())
				if code == 200 || code == 503 {
					if err != nil || h.Status != status {
						t.Fatalf("%#v %v", h, err)
					}
				} else if err == nil {
					t.Fatal("expected HTTP error")
				}
			})
		}
	}
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("body bound gzip=%v", compressed), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := `{"status":"UP","ignored":"` + strings.Repeat("x", micronautHealthMaxResponseBytes) + `"}`
				if compressed {
					w.Header().Set("Content-Encoding", "gzip")
					g := gzip.NewWriter(w)
					defer g.Close()
					fmt.Fprint(g, body)
				} else {
					fmt.Fprint(w, body)
				}
			}))
			defer server.Close()
			c, _ := NewMicronautHealthClient(server.URL, time.Second, nil)
			if _, err := c.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("expected body bound, got %v", err)
			}
		})
	}
	t.Run("bounded HTTP error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			fmt.Fprint(w, strings.Repeat("x", 3000))
		}))
		defer server.Close()
		c, _ := NewMicronautHealthClient(server.URL, time.Second, nil)
		_, err := c.Fetch(context.Background())
		if err == nil || len(err.Error()) > 1100 {
			t.Fatalf("unbounded error: %v", err)
		}
	})
}

func TestMicronautHealthRedirectsAndDeadline(t *testing.T) {
	for _, redirects := range []int{3, 4} {
		t.Run(fmt.Sprint(redirects), func(t *testing.T) {
			requests := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "p" {
					t.Error("redirect lost auth")
				}
				if requests <= redirects {
					http.Redirect(w, r, fmt.Sprintf("/%d", requests), 302)
					return
				}
				fmt.Fprint(w, `{"status":"UP"}`)
			}))
			defer s.Close()
			c, _ := NewMicronautHealthClient(s.URL, time.Second, &BasicAuth{Username: "u", Password: "p"})
			_, err := c.Fetch(context.Background())
			if (err != nil) != (redirects == 4) || requests != 4 {
				t.Fatalf("requests=%d err=%v", requests, err)
			}
		})
	}
	t.Run("cross origin", func(t *testing.T) {
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cross-origin request leaked") }))
		defer other.Close()
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
		defer s.Close()
		c, _ := NewMicronautHealthClient(s.URL, time.Second, nil)
		if _, err := c.Fetch(context.Background()); err == nil {
			t.Fatal("expected redirect rejection")
		}
	})
	for _, useContext := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline context=%v", useContext), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			defer s.Close()
			timeout := 20 * time.Millisecond
			ctx := context.Background()
			if useContext {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
				timeout = time.Second
			}
			c, _ := NewMicronautHealthClient(s.URL, timeout, nil)
			if _, err := c.Fetch(ctx); err == nil {
				t.Fatal("expected timeout")
			}
		})
	}
}

func TestMicronautHealthCapabilityLifecycle(t *testing.T) {
	type step struct {
		start                  int
		metricsFail            bool
		code                   int
		body, app, db, warning string
		probes                 int
	}
	tests := []struct {
		name     string
		optional bool
		steps    []step
	}{
		{"absent until restart", true, []step{
			{start: 1770000000, code: 404, probes: 1},
			{start: 1770000000, code: 200, body: `{"status":"UP"}`, probes: 1},
			{start: 1770000060, code: 200, body: `{"status":"UP"}`, app: "UP", probes: 2},
		}},
		{"known loss and recovery", true, []step{
			{start: 1770000000, code: 200, body: `{"status":"UP","details":{"jdbc":{"status":"UP"}}}`, app: "UP", db: "UP", probes: 1},
			{start: 1770000000, code: 404, warning: "health_fetch_failed", probes: 2},
			{start: 1770000000, code: 200, body: `{"status":"DOWN"}`, app: "DOWN", probes: 3},
		}},
		{"explicit always probes", false, []step{
			{start: 1770000000, code: 404, warning: "health_fetch_failed", probes: 1},
			{start: 1770000000, code: 404, warning: "health_fetch_failed", probes: 2},
		}},
		{"failed metrics cannot cache absence", true, []step{
			{metricsFail: true, code: 404, warning: "health_fetch_failed", probes: 1},
			{start: 1770000000, code: 200, body: `{"status":"UP"}`, app: "UP", probes: 2},
			{metricsFail: true, code: 503, body: `{"status":"DOWN","details":{"jdbc":{"status":"UP"}}}`, app: "DOWN", db: "UP", probes: 3},
		}},
		{"retry errors and partial details", true, []step{
			{start: 1770000000, code: 401, warning: "health_fetch_failed", probes: 1},
			{start: 1770000000, code: 200, body: `{"status":"UNKNOWN"}`, warning: "health_fetch_failed", probes: 2},
			{start: 1770000000, code: 200, body: `{"status":"UP","details":[]}`, app: "UP", warning: "health_partial", probes: 3},
			{start: 1770000000, code: 404, warning: "health_fetch_failed", probes: 4},
			{start: 1770000000, code: 200, body: `{"status":"UP"}`, app: "UP", probes: 5},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var current step
			probes, metrics := 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/prometheus" {
					metrics++
					if current.metricsFail {
						http.Error(w, "failed", 500)
						return
					}
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprintf(w, "process_start_time_seconds %d\nprocess_cpu_usage 0.2\n", current.start)
					return
				}
				probes++
				if metrics <= probes-1 {
					t.Error("health preceded metrics")
				}
				w.WriteHeader(current.code)
				fmt.Fprint(w, current.body)
			}))
			defer s.Close()
			pc, _ := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
			hc, _ := NewMicronautHealthClient(s.URL+"/health", time.Second, nil)
			if tt.optional {
				hc.TreatNotFoundAsOptional()
			}
			c := NewMicronautCollector("app", s.URL+"/prometheus", pc, hc)
			for i, st := range tt.steps {
				current = st
				r, err := c.Collect(context.Background())
				if (err != nil) != st.metricsFail || r.HealthStatus != st.app || r.DBHealthStatus != st.db || probes != st.probes {
					t.Fatalf("step %d: result=%#v err=%v probes=%d", i, r, err, probes)
				}
				warning := ""
				for _, e := range r.Events {
					if e.Severity == EventSeverityWarning {
						warning = e.Type
					}
				}
				if warning != st.warning {
					t.Fatalf("step %d events=%#v want %q", i, r.Events, st.warning)
				}
				if !st.metricsFail && len(r.Samples) != 2 {
					t.Fatalf("health invalidated metrics: %#v", r)
				}
			}
		})
	}
}

func TestMicronautHealthClientRejectsInvalidURLs(t *testing.T) {
	for _, raw := range []string{" http://example.com/health", "http://example.com/health ", "ftp://example.com/health", "http:///health", "http://user:secret@example.com/health", "http://example.com/health#", "http:health"} {
		if _, err := NewMicronautHealthClient(raw, time.Second, nil); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := NewMicronautHealthClient("http://example.com/health", 0, nil); err == nil {
		t.Fatal("accepted nonpositive timeout")
	}
}
