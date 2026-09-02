package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseQuarkusHealthResponseDiagnosesApplicationAndDatabase(t *testing.T) {
	health, err := ParseQuarkusHealthResponse([]byte(`{
  "status":"DOWN",
  "checks":[
    {"name":"Simple health check","status":"UP"},
    {"name":"Database connections health check","status":"DOWN","data":{"<default>":"DOWN"}},
    {"name":"Reactive PostgreSQL connections health check","status":"UP","data":{"reporting":"UP"}}
  ]
}`))
	if err != nil {
		t.Fatalf("ParseQuarkusHealthResponse() error = %v", err)
	}
	if health.Status != "DOWN" || health.DBStatus() != "DOWN" {
		t.Fatalf("health = %q/%q, want DOWN/DOWN", health.Status, health.DBStatus())
	}
}

func TestParseQuarkusHealthResponseLeavesDatabaseUnknownWithoutDatasourceCheck(t *testing.T) {
	health, err := ParseQuarkusHealthResponse([]byte(`{"status":"UP","checks":[{"name":"Simple health check","status":"UP"}]}`))
	if err != nil {
		t.Fatalf("ParseQuarkusHealthResponse() error = %v", err)
	}
	if health.Status != "UP" || health.DBStatus() != "" {
		t.Fatalf("health = %q/%q, want UP/empty", health.Status, health.DBStatus())
	}
}

func TestParseQuarkusHealthResponseRecognizesReactiveDatasourceChecks(t *testing.T) {
	health, err := ParseQuarkusHealthResponse([]byte(`{"status":"UP","checks":[{"name":"Reactive PostgreSQL connections health check","status":"UP","data":{"<default>":"UP"}}]}`))
	if err != nil {
		t.Fatalf("ParseQuarkusHealthResponse() error = %v", err)
	}
	if health.DBStatus() != "UP" {
		t.Fatalf("DBStatus() = %q, want UP", health.DBStatus())
	}
}

func TestParseQuarkusHealthResponseRejectsInvalidStatuses(t *testing.T) {
	for _, body := range []string{
		`{"checks":[]}`,
		`{"status":"WARN","checks":[]}`,
		`{"status":"UP","checks":[{"name":"db","status":"WARN"}]}`,
	} {
		if _, err := ParseQuarkusHealthResponse([]byte(body)); err == nil {
			t.Fatalf("ParseQuarkusHealthResponse(%s) error = nil, want invalid status", body)
		}
	}
}

func TestQuarkusHealthClientAcceptsDownResponseAndUsesBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"DOWN","checks":[{"name":"Database connections health check","status":"DOWN"}]}`))
	}))
	defer server.Close()

	client, err := NewQuarkusHealthClient(server.URL, time.Second, &BasicAuth{Username: "user", Password: "secret"})
	if err != nil {
		t.Fatalf("NewQuarkusHealthClient() error = %v", err)
	}
	health, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if health.Status != "DOWN" || health.DBStatus() != "DOWN" {
		t.Fatalf("health = %q/%q, want DOWN/DOWN", health.Status, health.DBStatus())
	}
}

func TestQuarkusHealthClientBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", quarkusHealthMaxResponseBytes+1)))
	}))
	defer server.Close()
	client, err := NewQuarkusHealthClient(server.URL, time.Second, nil)
	if err != nil {
		t.Fatalf("NewQuarkusHealthClient() error = %v", err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Fetch() error = %v, want response limit", err)
	}
}

func TestQuarkusHealthClientBoundsHTTPErrorExcerpt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", quarkusHealthMaxResponseBytes)))
	}))
	defer server.Close()
	client, err := NewQuarkusHealthClient(server.URL, time.Second, nil)
	if err != nil {
		t.Fatalf("NewQuarkusHealthClient() error = %v", err)
	}
	_, err = client.Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch() error = nil, want HTTP error")
	}
	if len(err.Error()) > quarkusHealthErrorExcerptBytes+128 {
		t.Fatalf("Fetch() error length = %d, want bounded error", len(err.Error()))
	}
}
