package collector

// This file fetches and normalizes Quarkus SmallRye Health responses.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const quarkusHealthMaxResponseBytes = 1 << 20
const quarkusHealthErrorExcerptBytes = 1024

var ErrQuarkusHealthNotFound = errors.New("Quarkus health endpoint not found")

type QuarkusHealthClient struct {
	url              string
	httpClient       *http.Client
	auth             *BasicAuth
	notFoundOptional bool
}

// TreatNotFoundAsOptional marks the conventional health endpoint as an
// optional capability. This is used when SmallRye Health is not installed.
func (c *QuarkusHealthClient) TreatNotFoundAsOptional() {
	c.notFoundOptional = true
}

type QuarkusHealthResponse struct {
	Status string               `json:"status"`
	Checks []QuarkusHealthCheck `json:"checks"`
}

type QuarkusHealthCheck struct {
	Name   string         `json:"name"`
	Status string         `json:"status"`
	Data   map[string]any `json:"data,omitempty"`
}

func NewQuarkusHealthClient(rawURL string, timeout time.Duration, auth *BasicAuth) (*QuarkusHealthClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("Quarkus health timeout must be positive")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parsing Quarkus health URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("Quarkus health URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("Quarkus health URL must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("Quarkus health URL must not include user information")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("Quarkus health URL must not include a fragment")
	}
	return &QuarkusHealthClient{
		url:        parsed.String(),
		httpClient: &http.Client{Timeout: timeout},
		auth:       auth,
	}, nil
}

func (c *QuarkusHealthClient) Fetch(ctx context.Context) (*QuarkusHealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Quarkus health request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.auth != nil {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Quarkus health: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, quarkusHealthMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading Quarkus health response: %w", err)
	}
	if len(body) > quarkusHealthMaxResponseBytes {
		return nil, fmt.Errorf("Quarkus health response exceeds %d byte limit", quarkusHealthMaxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		excerpt := boundedHealthErrorExcerpt(body)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: HTTP 404: %s", ErrQuarkusHealthNotFound, excerpt)
		}
		return nil, fmt.Errorf("Quarkus health returned HTTP %d: %s", resp.StatusCode, excerpt)
	}
	return ParseQuarkusHealthResponse(body)
}

func boundedHealthErrorExcerpt(body []byte) string {
	excerpt := strings.TrimSpace(string(body))
	if len(excerpt) > quarkusHealthErrorExcerptBytes {
		return excerpt[:quarkusHealthErrorExcerptBytes] + "..."
	}
	return excerpt
}

func ParseQuarkusHealthResponse(body []byte) (*QuarkusHealthResponse, error) {
	var health QuarkusHealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, fmt.Errorf("parsing Quarkus health response: %w", err)
	}
	health.Status = strings.ToUpper(strings.TrimSpace(health.Status))
	if !validQuarkusHealthStatus(health.Status) {
		return nil, fmt.Errorf("Quarkus health response has invalid status %q", health.Status)
	}
	for i := range health.Checks {
		health.Checks[i].Status = strings.ToUpper(strings.TrimSpace(health.Checks[i].Status))
		if !validQuarkusHealthStatus(health.Checks[i].Status) {
			return nil, fmt.Errorf("Quarkus health check %q has invalid status %q", health.Checks[i].Name, health.Checks[i].Status)
		}
	}
	return &health, nil
}

func (h *QuarkusHealthResponse) DBStatus() string {
	if h == nil {
		return ""
	}
	found := false
	status := "UP"
	for _, check := range h.Checks {
		name := strings.ToLower(strings.TrimSpace(check.Name))
		if !quarkusDatabaseHealthCheckName(name) {
			continue
		}
		found = true
		if check.Status == "DOWN" {
			status = "DOWN"
		}
	}
	if !found {
		return ""
	}
	return status
}

func quarkusDatabaseHealthCheckName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.Contains(name, "health check") {
		return false
	}
	if strings.Contains(name, "database") || strings.Contains(name, "datasource") {
		return true
	}
	for _, database := range []string{"postgresql", "mysql", "mariadb", "oracle", "mssql", "db2"} {
		if strings.Contains(name, database+" connections") {
			return true
		}
	}
	return false
}

func validQuarkusHealthStatus(status string) bool {
	return status == "UP" || status == "DOWN"
}
