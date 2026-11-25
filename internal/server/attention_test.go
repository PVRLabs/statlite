package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestAttentionEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name             string
		requests, errors float64
		stale            bool
		warning, cleared bool
	}{
		{name: "threshold inclusive", requests: 100, errors: 10, warning: true},
		{name: "normal", requests: 200, errors: 10, cleared: true},
		{name: "low requests with elevated rate", requests: 90, errors: 10},
		{name: "low requests with low rate", requests: 90, errors: 1},
		{name: "zero requests"},
		{name: "low errors", requests: 100, errors: 9, cleared: true},
		{name: "stale", requests: 100, errors: 10, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, now := attentionFixture(t, []string{"alpha", "beta"}, 0, tc.requests, tc.errors)
			if tc.stale {
				s.FreezeDashboardTime(now.Add(3 * time.Minute))
			}
			path := "/api/attention?target=alpha"
			w := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			var response attentionResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Target != "alpha" || (len(response.Conditions) > 0) != tc.warning || (len(response.ClearedChecks) > 0) != tc.cleared {
				t.Fatalf("unexpected response: %s", w.Body)
			}
			if tc.warning {
				c := response.Conditions[0]
				if c.Requests != tc.requests || c.Errors != tc.errors || c.Rate != tc.errors/tc.requests || c.Severity != "warning" || c.End.Sub(c.Start) != 10*time.Minute {
					t.Fatalf("incorrect evidence: %+v", c)
				}
			}
			for _, target := range []string{"", "beta", "unknown"} {
				w = httptest.NewRecorder()
				s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/attention?target="+target, nil))
				if target == "unknown" && w.Code != 404 {
					t.Fatalf("unknown status %d", w.Code)
				}
				if target == "beta" && (w.Code != 200 || w.Body.String() != "{\"conditions\":[],\"target\":\"beta\"}\n") {
					t.Fatalf("wrong target evidence: %s", w.Body)
				}
				if target == "" && (w.Code != 200 || w.Body.String() == "") {
					t.Fatalf("default target: %d %s", w.Code, w.Body)
				}
			}
		})
	}
}

func TestAttentionGlobalThresholds(t *testing.T) {
	fixture, _, now := attentionFixture(t, []string{"alpha", "beta"}, 1, 200, 10)
	s := NewWithManagerRetentionCutoffAndFilesystem("127.0.0.1:0", fixture.manager, 0, nil, "", config.AttentionConfig{
		ErrorRatePercent: new(4.0), MinRequests: new(200), MinErrors: new(10),
	})
	s.FreezeDashboardTime(now)
	for _, target := range []string{"alpha", "beta"} {
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/attention?target="+target, nil))
		var response attentionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 ||
			response.Target != target || len(response.Conditions) != 1 || response.Conditions[0].Threshold != .04 {
			t.Fatalf("%s response %d: %s", target, w.Code, w.Body)
		}
	}
}

func TestAttentionGlobalDisable(t *testing.T) {
	enabled, store, now := attentionFixture(t, []string{"alpha"}, 0, 100, 10)
	disabled := NewWithManagerRetentionCutoffAndFilesystem("127.0.0.1:0", enabled.manager, 0, nil, "", config.AttentionConfig{
		Enabled: new(false), ErrorRatePercent: new(5.0),
	})
	disabled.FreezeDashboardTime(now)
	get := func(s *Server, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	for _, tc := range []struct {
		server    *Server
		wantStrip bool
	}{{enabled, true}, {disabled, false}} {
		w := get(tc.server, "/")
		if w.Code != 200 || strings.Contains(w.Body.String(), `id="attention-strip"`) != tc.wantStrip ||
			!strings.Contains(w.Body.String(), `id="metrics-section"`) || strings.Contains(w.Body.String(), "{{dashboard_attention}}") {
			t.Fatalf("unexpected dashboard response for Attention enabled=%v, status=%d", tc.wantStrip, w.Code)
		}
	}

	summary := func(s *Server) any {
		t.Helper()
		w := get(s, "/api/summary?target=alpha")
		var targetFields struct {
			Targets             []monitor.TargetSummary `json:"targets"`
			SelectedTarget      monitor.TargetMetadata  `json:"selected_target"`
			Monitor             monitor.Status          `json:"monitor"`
			Latest              json.RawMessage         `json:"latest"`
			LatestRestart       *time.Time              `json:"latest_restart"`
			LatestRestartStatus string                  `json:"latest_restart_status"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &targetFields); err != nil || w.Code != 200 {
			t.Fatalf("summary response %d: %s", w.Code, w.Body)
		}
		return targetFields
	}
	if on, off := summary(enabled), summary(disabled); !reflect.DeepEqual(on, off) {
		t.Fatalf("target summary changed with Attention disabled: enabled %+v, disabled %+v", on, off)
	}

	for _, path := range []string{"/api/series?target=alpha", "/api/events?target=alpha"} {
		on, off := get(enabled, path), get(disabled, path)
		if on.Code != 200 || off.Code != 200 || on.Body.String() != off.Body.String() {
			t.Fatalf("%s changed with Attention disabled: enabled %d %s, disabled %d %s", path, on.Code, on.Body, off.Code, off.Body)
		}
	}

	// Custom thresholds cannot enable evaluation when Attention is disabled.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	w := get(disabled, "/api/attention?target=alpha")
	if w.Code != 404 || w.Body.String() != "404 page not found\n" {
		t.Fatalf("global disable %d: %s", w.Code, w.Body)
	}
}

func attentionFixture(t testing.TB, names []string, oldPolls int, requests, errors float64) (*Server, *storage.Store, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "attention.sqlite")
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var identities []storage.TargetIdentity
	var targets []monitor.ManagedTarget
	for _, name := range names {
		identities = append(identities, storage.TargetIdentity{Name: name, Type: "spring"})
		mon, err := monitor.New(name, &noopCollector{}, store, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, monitor.ManagedTarget{Metadata: monitor.TargetMetadata{Name: name, Type: "spring"}, Monitor: mon})
	}
	if err := store.RegisterTargets(t.Context(), identities); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, name := range names {
		for i := -oldPolls; i <= 20; i++ {
			if oldPolls == 0 && name != "alpha" {
				break
			}
			at := now.Add(time.Duration(i-20) * 30 * time.Second)
			n := float64(i + oldPolls)
			if _, err := store.SaveCollectionResult(t.Context(), attentionPoll(name, at, n*requests/20, n*errors/20)); err != nil {
				t.Fatal(err)
			}
		}
	}
	manager, err := monitor.NewManager(targets)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithManager("127.0.0.1:0", manager)
	s.FreezeDashboardTime(now)
	return s, store, now
}

func attentionPoll(target string, at time.Time, requests, errors float64) *collector.CollectionResult {
	result := serverTestResult(target, at, requests, requests/10, nil)
	result.Samples = append(result.Samples, collector.MetricSample{Key: "http_5xx_total", Kind: collector.MetricKindCounter, Value: errors, Unit: "requests"})
	return result
}

// Compare two simultaneous dashboard sessions, plus one collection write per
// simulated refresh. Report handler latency and allocations independently too.
func BenchmarkAttention(b *testing.B) {
	for _, count := range []int{1, 3} {
		b.Run(fmt.Sprintf("targets%d", count), func(b *testing.B) {
			names := []string{"alpha", "beta", "gamma"}[:count]
			s, store, now := attentionFixture(b, names, 2880, 200, 30)
			do := func(path string) {
				w := httptest.NewRecorder()
				s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != 200 {
					b.Errorf("%s: %d %s", path, w.Code, w.Body)
				}
			}
			b.Run("query", func(b *testing.B) {
				mon := s.manager.PrimaryMonitor()
				b.ReportAllocs()
				for b.Loop() {
					window, err := mon.AttentionHTTP5xx(b.Context(), now.Add(-10*time.Minute), now)
					if err != nil || !window.Adequate {
						b.Fatalf("query: %+v %v", window, err)
					}
				}
			})
			b.Run("endpoint", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					do("/api/attention?target=alpha")
				}
			})
			for _, withAttention := range []bool{false, true} {
				b.Run(fmt.Sprintf("dashboard/attention=%t", withAttention), func(b *testing.B) {
					b.ReportAllocs()
					tick := 0
					for b.Loop() {
						tick++
						var wg sync.WaitGroup
						wg.Go(func() {
							name := names[tick%count]
							at := now.Add(time.Duration(tick) * 30 * time.Second)
							if _, err := store.SaveCollectionResult(b.Context(), attentionPoll(name, at, 30000, 4500)); err != nil {
								b.Error(err)
							}
						})
						for session := range 2 {
							query := "?target=" + names[session%count] + "&range=1h"
							for _, route := range []string{"/api/summary", "/api/series", "/api/events"} {
								wg.Go(func() { do(route + query) })
							}
							if withAttention {
								wg.Go(func() { do("/api/attention" + query) })
							}
						}
						wg.Wait()
					}
				})
			}
		})
	}
}

func TestAttentionBurstReplay(t *testing.T) {
	s, store, now := attentionFixture(t, []string{"alpha"}, 0, 200, 0)
	check := func(tick int, wantWarning bool) {
		t.Helper()
		s.FreezeDashboardTime(now.Add(time.Duration(tick) * 30 * time.Second))
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/attention", nil))
		var result attentionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
			t.Fatalf("tick %d: %d %s (%v)", tick, w.Code, w.Body, err)
		}
		if (len(result.Conditions) != 0) != wantWarning {
			t.Errorf("tick %d: unexpected warning state: %s", tick, w.Body)
		}
	}

	check(0, false)
	var totalErrors float64
	for tick := 1; tick <= 27; tick++ {
		if tick >= 3 && tick <= 5 {
			totalErrors += 10
		}
		at := now.Add(time.Duration(tick) * 30 * time.Second)
		result := attentionPoll("alpha", at, 200+float64(tick*10), totalErrors)
		if _, err := store.SaveCollectionResult(t.Context(), result); err != nil {
			t.Fatal(err)
		}
		check(tick, tick >= 4 && tick <= 23)
	}
}
