package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestActuatorClientFetchesHealthWithBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actuator/health" {
			t.Fatalf("path = %q, want /actuator/health", r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "pass" {
			t.Fatalf("basic auth = %q/%q/%v, want user/pass/true", username, password, ok)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "UP",
			"components": map[string]interface{}{
				"db": map[string]string{"status": "UP"},
			},
		})
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, &BasicAuth{
		Username: "user",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	health, err := client.FetchHealth(context.Background())
	if err != nil {
		t.Fatalf("FetchHealth() error = %v", err)
	}
	if health.Status != "UP" {
		t.Fatalf("health status = %q, want UP", health.Status)
	}
	if health.DBStatus() != "UP" {
		t.Fatalf("DBStatus() = %q, want UP", health.DBStatus())
	}
}

func TestActuatorClientFetchesMetricWithTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/actuator/metrics/jvm.memory.used" {
			t.Fatalf("path = %q, want /actuator/metrics/jvm.memory.used", r.URL.Path)
		}
		tags := r.URL.Query()["tag"]
		if len(tags) != 1 || tags[0] != "area:heap" {
			t.Fatalf("tags = %#v, want area:heap", tags)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":     "jvm.memory.used",
			"baseUnit": "bytes",
			"measurements": []map[string]interface{}{
				{"statistic": "VALUE", "value": 123},
			},
			"availableTags": []map[string]interface{}{
				{"tag": "area", "values": []string{"heap", "nonheap"}},
			},
		})
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	metric, err := client.FetchMetric(context.Background(), "jvm.memory.used", []string{"area:heap"})
	if err != nil {
		t.Fatalf("FetchMetric() error = %v", err)
	}
	if metric.Name != "jvm.memory.used" {
		t.Fatalf("metric name = %q, want jvm.memory.used", metric.Name)
	}
	if len(metric.Measurements) != 1 || metric.Measurements[0].Value != 123 {
		t.Fatalf("measurements = %#v, want value 123", metric.Measurements)
	}
}

func TestActuatorClientReturnsClearHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing metric", http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	_, err = client.FetchMetric(context.Background(), "missing.metric", nil)
	if err == nil {
		t.Fatal("FetchMetric() error = nil, want error")
	}
}

func TestActuatorClientPropagatesContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := client.FetchHealth(ctx)
		errCh <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("server did not receive request")
	}
	cancel()

	err = <-errCh
	if err == nil {
		t.Fatal("FetchHealth() error = nil, want canceled context error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("FetchHealth() error = %q, want context canceled", err)
	}

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("server did not observe request context cancellation")
	}
}

func TestActuatorClientReturnsMalformedJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":`))
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	_, err = client.FetchHealth(context.Background())
	if err == nil {
		t.Fatal("FetchHealth() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "parsing actuator health response") {
		t.Fatalf("FetchHealth() error = %q, want parsing context", err)
	}
}
