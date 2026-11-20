package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
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
		stale, disabled  bool
		warning, cleared bool
	}{
		{name: "threshold inclusive", requests: 100, errors: 10, warning: true},
		{name: "normal", requests: 200, errors: 10, cleared: true},
		{name: "low requests with elevated rate", requests: 90, errors: 10},
		{name: "low requests with low rate", requests: 90, errors: 1},
		{name: "zero requests"},
		{name: "low errors", requests: 100, errors: 9, cleared: true},
		{name: "stale", requests: 100, errors: 10, stale: true},
		{name: "disabled", disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, now := attentionFixture(t, []string{"alpha", "beta"}, 0, tc.requests, tc.errors)
			if tc.stale {
				s.FreezeDashboardTime(now.Add(3 * time.Minute))
			}
			if tc.disabled {
				s.SetAttentionEnabled(false)
				// Closed SQLite proves there is no history dependency on this path.
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			path := "/api/attention?target=alpha"
			if tc.disabled {
				path = "/api/attention?target=unknown"
			}
			w := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tc.disabled {
				if w.Body.String() != "{\"conditions\":[]}\n" {
					t.Fatalf("disabled body: %s", w.Body)
				}
				return
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

func TestAttentionTargetOverrides(t *testing.T) {
	s, store, _ := attentionFixture(t, []string{"alpha", "beta"}, 0, 200, 10)
	alpha, _ := s.manager.ExactTarget("alpha")
	beta, _ := s.manager.ExactTarget("beta")
	rate, requests, errors := 5.0, 200, 10
	alpha.AttentionHTTP5xx = config.HTTP5xxAttentionConfig{
		Enabled: new(true), ErrorRatePercent: &rate, MinRequests: &requests, MinErrors: &errors,
	}
	beta.AttentionHTTP5xx.Enabled = new(false)
	manager, err := monitor.NewManager([]monitor.ManagedTarget{alpha, beta})
	if err != nil {
		t.Fatal(err)
	}
	s.manager = manager
	get := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/attention?target="+target, nil))
		return w
	}
	w := get("alpha")
	var response attentionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 ||
		len(response.Conditions) != 1 || response.Conditions[0].Threshold != .05 {
		t.Fatalf("override response %d: %s", w.Code, w.Body)
	}

	// Disable at either level must skip the closed history store.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	w = get("beta")
	if w.Code != 200 || w.Body.String() != "{\"conditions\":[],\"target\":\"beta\"}\n" {
		t.Fatalf("target disabled %d: %s", w.Code, w.Body)
	}
	s.SetAttentionEnabled(false)
	w = get("alpha")
	if w.Code != 200 || w.Body.String() != "{\"conditions\":[]}\n" {
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
