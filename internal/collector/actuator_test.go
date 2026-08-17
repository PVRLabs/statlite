package collector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestActuatorClientEffectiveURLCanonicalizesRepeatedTags(t *testing.T) {
	client, err := NewActuatorClient("https://example.com/base/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	query := url.Values{}
	query.Add("tag", "area:heap")
	query.Add("tag", "id:alpha beta")
	got := client.effectiveURL("metrics/jvm.memory.used", query)
	want := "https://example.com/base/actuator/metrics/jvm.memory.used?tag=area%3Aheap&tag=id%3Aalpha+beta"
	if got != want {
		t.Fatalf("effectiveURL() = %q, want %q", got, want)
	}
}

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

func TestActuatorClientFetchesUnhealthyHealthFromNon2xxResponse(t *testing.T) {
	for _, healthStatus := range []string{"DOWN", "OUT_OF_SERVICE"} {
		t.Run(healthStatus, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"status": healthStatus})
			}))
			defer server.Close()

			client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
			if err != nil {
				t.Fatalf("NewActuatorClient() error = %v", err)
			}

			health, err := client.FetchHealth(context.Background())
			if err != nil {
				t.Fatalf("FetchHealth() error = %v, want non-2xx health response to be accepted", err)
			}
			if health.Status != healthStatus {
				t.Fatalf("health status = %q, want %q", health.Status, healthStatus)
			}
		})
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

func TestActuatorClientClassifiesFailures(t *testing.T) {
	tests := []struct {
		name       string
		response   springScriptedResponse
		transport  error
		wantClass  actuatorFailureClass
		wantStatus int
		wantCause  string
	}{
		{name: "authentication", response: springScriptedResponse{status: http.StatusUnauthorized, body: `{"error":"Unauthorized"}`}, wantClass: actuatorFailureAuthentication, wantStatus: http.StatusUnauthorized},
		{name: "missing endpoint", response: springScriptedResponse{status: http.StatusNotFound, body: "not found\n"}, wantClass: actuatorFailureNotFound, wantStatus: http.StatusNotFound},
		{name: "empty HTTP body", response: springScriptedResponse{status: http.StatusNotFound}, wantClass: actuatorFailureNotFound, wantStatus: http.StatusNotFound, wantCause: "Not Found"},
		{name: "other HTTP status", response: springScriptedResponse{status: http.StatusBadGateway, body: "upstream unavailable\n"}, wantClass: actuatorFailureHTTP, wantStatus: http.StatusBadGateway},
		{name: "malformed JSON", response: springOK(`{"name":`), wantClass: actuatorFailureMalformed},
		{name: "transport", transport: errors.New("network unavailable"), wantClass: actuatorFailureTransport},
		{name: "response size", response: springOK(strings.Repeat("x", actuatorMaxResponseBytes+1)), wantClass: actuatorFailureResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &springRecordingTransport{
				responses: map[string]springScriptedResponse{
					"/actuator/metrics/example.metric": tt.response,
				},
				transportError: tt.transport,
			}
			client, err := NewActuatorClient("http://spring.test/actuator", time.Second, nil)
			if err != nil {
				t.Fatalf("NewActuatorClient() error = %v", err)
			}
			client.httpClient.Transport = transport

			_, err = client.FetchMetric(context.Background(), "example.metric", nil)
			if err == nil {
				t.Fatal("FetchMetric() error = nil, want structured failure")
			}
			var failure *actuatorFailure
			if !errors.As(err, &failure) {
				t.Fatalf("FetchMetric() error type = %T, want *actuatorFailure", err)
			}
			if failure.class != tt.wantClass || failure.statusCode != tt.wantStatus {
				t.Fatalf("failure identity = %q/HTTP %d, want %q/HTTP %d", failure.class, failure.statusCode, tt.wantClass, tt.wantStatus)
			}
			if failure.endpoint != "/actuator/metrics/example.metric" {
				t.Fatalf("failure endpoint = %q, want /actuator/metrics/example.metric", failure.endpoint)
			}
			if failure.cause == "" {
				t.Fatal("failure cause is empty")
			}
			if tt.wantCause != "" && failure.cause != tt.wantCause {
				t.Fatalf("failure cause = %q, want %q", failure.cause, tt.wantCause)
			}
		})
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchHealth() error = %v, want errors.Is(context.Canceled)", err)
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

func TestActuatorClientEnforcesResponseSizeLimit(t *testing.T) {
	validJSON := `{"status":"UP"}`
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact limit", size: actuatorMaxResponseBytes},
		{name: "limit plus one", size: actuatorMaxResponseBytes + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := validJSON + strings.Repeat(" ", tt.size-len(validJSON))
			if len(body) != tt.size {
				t.Fatalf("body size = %d, want %d", len(body), tt.size)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			client, err := NewActuatorClient(server.URL, time.Second, nil)
			if err != nil {
				t.Fatalf("NewActuatorClient() error = %v", err)
			}

			health, err := client.FetchHealth(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("FetchHealth() error = nil, want response-size error")
				}
				if !strings.Contains(err.Error(), "response exceeds 1048576 byte limit") {
					t.Fatalf("FetchHealth() error = %q, want response-size context", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchHealth() error = %v", err)
			}
			if health.Status != "UP" {
				t.Fatalf("health status = %q, want UP", health.Status)
			}
		})
	}
}
