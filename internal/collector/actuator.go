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

type actuatorRawResult struct {
	body         []byte
	statusCode   int
	effectiveURL string
	err          error
}

type actuatorFailureClass string

const (
	actuatorFailureAuthentication actuatorFailureClass = "authentication"
	actuatorFailureNotFound       actuatorFailureClass = "not_found"
	actuatorFailureHTTP           actuatorFailureClass = "http"
	actuatorFailureMalformed      actuatorFailureClass = "malformed_json"
	actuatorFailureTransport      actuatorFailureClass = "transport"
	actuatorFailureResponse       actuatorFailureClass = "response"
)

type actuatorFailure struct {
	class        actuatorFailureClass
	endpoint     string
	statusCode   int
	cause        string
	displayError string
	underlying   error
}

func (e *actuatorFailure) Error() string {
	return e.displayError
}

func (e *actuatorFailure) Unwrap() error {
	return e.underlying
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
	if auth == nil && parsed.User != nil {
		password, _ := parsed.User.Password()
		auth = &BasicAuth{Username: parsed.User.Username(), Password: password}
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
	return c.decodeHealth(c.fetchRaw(ctx, "health", nil))
}

// getHealthJSON accepts a valid Spring Boot health payload even when its HTTP
// status is non-2xx. Spring Boot maps DOWN and OUT_OF_SERVICE health statuses
// to 503 by default; those statuses describe the monitored application rather
// than a failure to poll it.
func (c *ActuatorClient) getHealthJSON(ctx context.Context, health *HealthResponse) error {
	endpointPath := "health"
	decoded, err := c.decodeHealth(c.fetchRaw(ctx, endpointPath, nil))
	if err != nil {
		return err
	}
	*health = *decoded
	return nil
}

func (c *ActuatorClient) decodeHealth(raw actuatorRawResult) (*HealthResponse, error) {
	endpointPath := "health"
	if raw.err != nil {
		return nil, raw.err
	}
	body, statusCode := raw.body, raw.statusCode
	var health HealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, newActuatorFailure(actuatorFailureMalformed, raw.effectiveURL, 0, err.Error(), fmt.Sprintf("parsing actuator %s response: %v", endpointPath, err), err)
	}
	if strings.TrimSpace(health.Status) == "" {
		if statusCode < 200 || statusCode >= 300 {
			return nil, newActuatorHTTPFailure(raw.effectiveURL, endpointPath, statusCode, body)
		}
	}
	setRaw(&health, body)
	return &health, nil
}

func (c *ActuatorClient) FetchMetric(ctx context.Context, name string, tags []string) (*MetricResponse, error) {
	query, err := metricQuery(name, tags)
	if err != nil {
		return nil, err
	}

	endpointPath := path.Join("metrics", name)
	return c.decodeMetric(endpointPath, c.fetchRaw(ctx, endpointPath, query))
}

func metricQuery(name string, tags []string) (url.Values, error) {
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
	return query, nil
}

func (h *HealthResponse) DBStatus() string {
	if h == nil {
		return ""
	}
	return findComponentStatus(h.Components, "db")
}

func (c *ActuatorClient) getJSON(ctx context.Context, endpointPath string, query url.Values, dest interface{}) error {
	raw := c.fetchRaw(ctx, endpointPath, query)
	if raw.err != nil {
		return raw.err
	}
	body, statusCode := raw.body, raw.statusCode
	if statusCode < 200 || statusCode >= 300 {
		return newActuatorHTTPFailure(raw.effectiveURL, endpointPath, statusCode, body)
	}

	if err := json.Unmarshal(body, dest); err != nil {
		return newActuatorFailure(actuatorFailureMalformed, raw.effectiveURL, 0, err.Error(), fmt.Sprintf("parsing actuator %s response: %v", endpointPath, err), err)
	}
	setRaw(dest, body)
	return nil
}

func (c *ActuatorClient) decodeMetric(endpointPath string, raw actuatorRawResult) (*MetricResponse, error) {
	if raw.err != nil {
		return nil, raw.err
	}
	if raw.statusCode < 200 || raw.statusCode >= 300 {
		return nil, newActuatorHTTPFailure(raw.effectiveURL, endpointPath, raw.statusCode, raw.body)
	}

	var metric MetricResponse
	if err := json.Unmarshal(raw.body, &metric); err != nil {
		return nil, newActuatorFailure(actuatorFailureMalformed, raw.effectiveURL, 0, err.Error(), fmt.Sprintf("parsing actuator %s response: %v", endpointPath, err), err)
	}
	setRaw(&metric, raw.body)
	return &metric, nil
}

func (c *ActuatorClient) fetchRaw(ctx context.Context, endpointPath string, query url.Values) actuatorRawResult {
	effectiveURL := c.effectiveURL(endpointPath, query)
	body, statusCode, err := c.fetch(ctx, endpointPath, query)
	return actuatorRawResult{body: body, statusCode: statusCode, effectiveURL: effectiveURL, err: err}
}

func (c *ActuatorClient) fetch(ctx context.Context, endpointPath string, query url.Values) ([]byte, int, error) {
	endpointURL := c.effectiveURL(endpointPath, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		message := fmt.Sprintf("creating actuator request: %v", err)
		return nil, 0, newActuatorFailure(actuatorFailureTransport, endpointURL, 0, err.Error(), message, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.auth != nil {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		message := fmt.Sprintf("fetching actuator %s: %v", endpointPath, err)
		return nil, 0, newActuatorFailure(actuatorFailureTransport, endpointURL, 0, err.Error(), message, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, actuatorMaxResponseBytes+1))
	if err != nil {
		message := fmt.Sprintf("reading actuator %s response: %v", endpointPath, err)
		return nil, 0, newActuatorFailure(actuatorFailureResponse, endpointURL, 0, err.Error(), message, err)
	}
	if len(body) > actuatorMaxResponseBytes {
		cause := fmt.Sprintf("response exceeds %d byte limit", actuatorMaxResponseBytes)
		message := fmt.Sprintf("actuator %s %s", endpointPath, cause)
		return nil, 0, newActuatorFailure(actuatorFailureResponse, endpointURL, 0, cause, message, nil)
	}
	return body, resp.StatusCode, nil
}

func newActuatorHTTPFailure(effectiveURL, endpointPath string, statusCode int, body []byte) error {
	class := actuatorFailureHTTP
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		class = actuatorFailureAuthentication
	case http.StatusNotFound:
		class = actuatorFailureNotFound
	}
	cause := strings.TrimSpace(string(body))
	if cause == "" {
		cause = http.StatusText(statusCode)
		if cause == "" {
			cause = "empty response body"
		}
	}
	message := fmt.Sprintf("actuator %s returned HTTP %d: %s", endpointPath, statusCode, cause)
	return newActuatorFailure(class, effectiveURL, statusCode, cause, message, nil)
}

func newActuatorFailure(class actuatorFailureClass, effectiveURL string, statusCode int, cause, displayError string, underlying error) error {
	return &actuatorFailure{
		class:        class,
		endpoint:     effectiveEndpoint(effectiveURL),
		statusCode:   statusCode,
		cause:        cause,
		displayError: displayError,
		underlying:   underlying,
	}
}

func effectiveEndpoint(effectiveURL string) string {
	parsed, err := url.Parse(effectiveURL)
	if err != nil {
		return effectiveURL
	}
	return parsed.RequestURI()
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
