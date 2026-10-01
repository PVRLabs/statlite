package collector

// This file fetches and normalizes Micronaut management health responses.

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

	"github.com/pvrlabs/statlite/internal/urlshape"
)

const micronautHealthMaxResponseBytes = 1 << 20
const micronautHealthErrorExcerptBytes = 1024

var ErrMicronautHealthNotFound = errors.New("Micronaut health endpoint not found")

type MicronautHealthClient struct {
	url              string
	httpClient       *http.Client
	auth             *BasicAuth
	notFoundOptional bool
}

// TreatNotFoundAsOptional marks the conventional health endpoint as an
// optional capability, such as when management health is disabled.
func (c *MicronautHealthClient) TreatNotFoundAsOptional() {
	c.notFoundOptional = true
}

// MicronautHealthResponse retains only fixed statuses and a bounded diagnostic.
type MicronautHealthResponse struct {
	Status         string
	DatabaseStatus string
	Warning        string
}

func NewMicronautHealthClient(rawURL string, timeout time.Duration, auth *BasicAuth) (*MicronautHealthClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("Micronaut health timeout must be positive")
	}
	if strings.TrimSpace(rawURL) != rawURL {
		return nil, errors.New("Micronaut health URL must not contain surrounding whitespace")
	}
	parsed, err := urlshape.ParseHTTP(rawURL)
	if err != nil {
		return nil, fmt.Errorf("Micronaut health URL: %w", err)
	}
	if parsed.User != nil {
		return nil, errors.New("Micronaut health URL must not include user information")
	}
	return &MicronautHealthClient{
		url: parsed.String(),
		httpClient: &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return errors.New("Micronaut health redirect limit exceeded")
			}
			if len(via) > 0 && !micronautHealthSameOrigin(req.URL, via[0].URL) {
				return errors.New("Micronaut health redirects may not change origin")
			}
			if auth != nil {
				req.SetBasicAuth(auth.Username, auth.Password)
			}
			return nil
		}},
		auth: auth,
	}, nil
}

func (c *MicronautHealthClient) Fetch(ctx context.Context) (*MicronautHealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Micronaut health request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.auth != nil {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching Micronaut health: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, micronautHealthMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading Micronaut health response: %w", err)
	}
	if len(body) > micronautHealthMaxResponseBytes {
		return nil, fmt.Errorf("Micronaut health response exceeds %d byte limit", micronautHealthMaxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		excerpt := boundedMicronautHealthErrorExcerpt(body)
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: HTTP 404: %s", ErrMicronautHealthNotFound, excerpt)
		}
		return nil, fmt.Errorf("Micronaut health returned HTTP %d: %s", resp.StatusCode, excerpt)
	}
	return ParseMicronautHealthResponse(body)
}

func boundedMicronautHealthErrorExcerpt(body []byte) string {
	excerpt := strings.TrimSpace(string(body))
	if len(excerpt) > micronautHealthErrorExcerptBytes {
		excerpt = excerpt[:micronautHealthErrorExcerptBytes]
	}
	return excerpt
}

func micronautHealthSameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// ParseMicronautHealthResponse validates application status first. Optional JDBC
// shape errors preserve that authoritative status; datasource children are ignored.
func ParseMicronautHealthResponse(body []byte) (*MicronautHealthResponse, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return nil, errors.New("Micronaut health response must be a single JSON object")
	}
	status := micronautHealthStatus(root["status"])
	if status == "" {
		return nil, errors.New("Micronaut health response has invalid status")
	}
	health := &MicronautHealthResponse{Status: status}
	optionalObject := func(raw json.RawMessage) (map[string]json.RawMessage, bool) {
		if len(raw) == 0 {
			return nil, true
		}
		var object map[string]json.RawMessage
		err := json.Unmarshal(raw, &object)
		return object, err == nil
	}
	details, ok := optionalObject(root["details"])
	if !ok {
		health.Warning = "Micronaut health details must be an object"
		return health, nil
	}
	jdbc, ok := optionalObject(details["jdbc"])
	if !ok {
		health.Warning = "Micronaut JDBC health must be an object"
		return health, nil
	}
	if jdbc != nil {
		health.DatabaseStatus = micronautHealthStatus(jdbc["status"])
		if health.DatabaseStatus == "" {
			health.Warning = "Micronaut JDBC health has invalid status"
		}
	}
	return health, nil
}

func micronautHealthStatus(raw json.RawMessage) string {
	var status string
	if json.Unmarshal(raw, &status) != nil {
		return ""
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != "UP" && status != "DOWN" {
		return ""
	}
	return status
}
