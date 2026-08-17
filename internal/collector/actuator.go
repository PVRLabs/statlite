package collector

// This file fetches Spring Boot Actuator health and metric endpoint responses.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const actuatorMaxResponseBytes = 1 << 20

type BasicAuth struct {
	Username string
	Password string
}

type ActuatorClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	auth       *BasicAuth
}

type HealthResponse struct {
	Status     string                     `json:"status"`
	Components map[string]HealthComponent `json:"components,omitempty"`
	Raw        json.RawMessage            `json:"raw,omitempty"`
}

type HealthComponent struct {
	Status     string                     `json:"status"`
	Components map[string]HealthComponent `json:"components,omitempty"`
}

type MetricResponse struct {
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	BaseUnit      string              `json:"baseUnit,omitempty"`
	Measurements  []MetricMeasurement `json:"measurements,omitempty"`
	AvailableTags []MetricTag         `json:"availableTags,omitempty"`
	Raw           json.RawMessage     `json:"raw,omitempty"`
}

type MetricMeasurement struct {
	Statistic string  `json:"statistic"`
	Value     float64 `json:"value"`
}

type MetricTag struct {
	Tag    string   `json:"tag"`
	Values []string `json:"values"`
}

func NewActuatorClient(baseURL string, timeout time.Duration, auth *BasicAuth) (*ActuatorClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("actuator timeout must be positive")
	}

	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parsing actuator base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("actuator base URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("actuator base URL must include a host")
	}

	return &ActuatorClient{
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		auth: auth,
	}, nil
}

func (c *ActuatorClient) FetchHealth(ctx context.Context) (*HealthResponse, error) {
	var health HealthResponse
	if err := c.getHealthJSON(ctx, &health); err != nil {
		return nil, err
	}
	return &health, nil
}

// getHealthJSON accepts a valid Spring Boot health payload even when its HTTP
// status is non-2xx. Spring Boot maps DOWN and OUT_OF_SERVICE health statuses
// to 503 by default; those statuses describe the monitored application rather
// than a failure to poll it.
func (c *ActuatorClient) getHealthJSON(ctx context.Context, health *HealthResponse) error {
	endpointPath := "health"
	body, statusCode, err := c.fetch(ctx, endpointPath, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, health); err != nil {
		return fmt.Errorf("parsing actuator %s response: %w", endpointPath, err)
	}
	if strings.TrimSpace(health.Status) == "" {
		if statusCode < 200 || statusCode >= 300 {
			return fmt.Errorf("actuator %s returned HTTP %d: %s", endpointPath, statusCode, strings.TrimSpace(string(body)))
		}
	}
	setRaw(health, body)
	return nil
}

func (c *ActuatorClient) FetchMetric(ctx context.Context, name string, tags []string) (*MetricResponse, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("metric name is required")
	}

	query := url.Values{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !strings.Contains(tag, ":") {
			return nil, fmt.Errorf("metric tag %q must use key:value format", tag)
		}
		query.Add("tag", tag)
	}

	var metric MetricResponse
	if err := c.getJSON(ctx, path.Join("metrics", name), query, &metric); err != nil {
		return nil, err
	}
	return &metric, nil
}

func (h *HealthResponse) DBStatus() string {
	if h == nil {
		return ""
	}
	return findComponentStatus(h.Components, "db")
}

func (c *ActuatorClient) getJSON(ctx context.Context, endpointPath string, query url.Values, dest interface{}) error {
	body, statusCode, err := c.fetch(ctx, endpointPath, query)
	if err != nil {
		return err
	}
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("actuator %s returned HTTP %d: %s", endpointPath, statusCode, strings.TrimSpace(string(body)))
	}

	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("parsing actuator %s response: %w", endpointPath, err)
	}
	setRaw(dest, body)
	return nil
}

func (c *ActuatorClient) fetch(ctx context.Context, endpointPath string, query url.Values) ([]byte, int, error) {
	endpointURL := c.effectiveURL(endpointPath, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating actuator request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.auth != nil {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching actuator %s: %w", endpointPath, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, actuatorMaxResponseBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("reading actuator %s response: %w", endpointPath, err)
	}
	if len(body) > actuatorMaxResponseBytes {
		return nil, 0, fmt.Errorf("actuator %s response exceeds %d byte limit", endpointPath, actuatorMaxResponseBytes)
	}
	return body, resp.StatusCode, nil
}

func (c *ActuatorClient) effectiveURL(endpointPath string, query url.Values) string {
	endpoint := *c.baseURL
	endpoint.Path = joinURLPath(c.baseURL.Path, endpointPath)
	endpoint.RawQuery = query.Encode()
	return endpoint.String()
}

func joinURLPath(basePath, endpointPath string) string {
	if basePath == "" || basePath == "/" {
		return "/" + endpointPath
	}
	return path.Join(basePath, endpointPath)
}

func setRaw(dest interface{}, body []byte) {
	switch v := dest.(type) {
	case *HealthResponse:
		v.Raw = append(v.Raw[:0], body...)
	case *MetricResponse:
		v.Raw = append(v.Raw[:0], body...)
	}
}

func findComponentStatus(components map[string]HealthComponent, name string) string {
	for key, component := range components {
		if strings.EqualFold(key, name) {
			return component.Status
		}
		if status := findComponentStatus(component.Components, name); status != "" {
			return status
		}
	}
	return ""
}
