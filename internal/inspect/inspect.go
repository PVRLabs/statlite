// Package inspect recognizes the small set of application integrations that
// StatLite can configure directly.
package inspect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

const (
	defaultTimeout     = 5 * time.Second
	maxResponseBytes   = 1 << 20
	maxRedirects       = 3
	statliteMetricPath = "statlite/metrics"
)

var errRedirectLimit = errors.New("inspect redirect limit exceeded")

// TargetType identifies a supported configuration target.
type TargetType string

const (
	TargetSpring          TargetType = "spring"
	TargetQuarkus         TargetType = "quarkus"
	TargetStatliteMetrics TargetType = "statlite-metrics"
)

type CompatibilityStatus string

const (
	CompatibilityCompatible CompatibilityStatus = "compatible"
	CompatibilityPartial    CompatibilityStatus = "partial"
)

// Result contains only information needed to render a suggested target.
type Result struct {
	TargetType   TargetType
	Endpoint     string
	Capabilities []string
	Warnings     []string
	Status       CompatibilityStatus
}

// FailureKind describes the small set of outcomes the command layer needs to
// turn into useful failure messages.
type FailureKind string

const (
	FailureUnrecognized    FailureKind = "unrecognized"
	FailureIncomplete      FailureKind = "incomplete"
	FailureAuthRequired    FailureKind = "authentication_required"
	FailureUnreachable     FailureKind = "unreachable"
	FailureIncompatible    FailureKind = "incompatible"
	FailureMultiple        FailureKind = "multiple_matches"
	FailureTypeUnsupported FailureKind = "type_unsupported"
	FailureTypeUnavailable FailureKind = "type_unavailable"
)

// Failure reports why inspection could not produce one confident target.
type Failure struct {
	Kind FailureKind
	Err  error
}

func (e *Failure) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *Failure) Unwrap() error { return e.Err }

type inspector struct {
	client  *http.Client
	timeout time.Duration
}

type probe struct {
	body       []byte
	statusCode int
	failure    FailureKind
	err        error
}

// Application probes one application base URL using the fixed StatLite
// discovery flow.
func Application(ctx context.Context, rawURL string) (*Result, error) {
	base, err := parseApplicationURL(rawURL)
	if err != nil {
		return nil, err
	}

	i := &inspector{
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return errRedirectLimit
				}
				return nil
			},
		},
		timeout: defaultTimeout,
	}
	return i.application(ctx, base)
}

// Inspect performs one explicit framework inspection. Application remains the
// separate conservative untyped discovery entry point.
func Inspect(ctx context.Context, targetType TargetType, rawURL string) (*Result, error) {
	switch targetType {
	case TargetSpring:
		return inspectSpring(ctx, rawURL)
	case TargetQuarkus:
		return inspectQuarkus(ctx, rawURL)
	default:
		return nil, &Failure{
			Kind: FailureTypeUnsupported,
			Err:  fmt.Errorf("unsupported inspection type %q (supported: quarkus)", targetType),
		}
	}
}

// inspectSpring is the framework-owned boundary for Spring inspection. Spring
// continues to use untyped discovery until its explicit source/Actuator
// inspection contract is defined.
func inspectSpring(context.Context, string) (*Result, error) {
	return nil, &Failure{
		Kind: FailureTypeUnavailable,
		Err:  errors.New("typed inspection type \"spring\" is not available (supported: quarkus)"),
	}
}

func (i *inspector) application(parent context.Context, base *url.URL) (*Result, error) {
	ctx, cancel := context.WithTimeout(parent, i.timeout)
	defer cancel()

	healthURL := appendPath(base, "actuator/health")
	statliteURL := appendPath(base, statliteMetricPath)
	quarkusURL := appendPath(base, "q/metrics")
	actuatorURL := appendPath(base, "actuator")

	healthProbe := i.get(ctx, healthURL)
	springMatched := recognizesSpringHealth(healthProbe)
	healthProbe.body = nil

	statliteProbe := i.get(ctx, statliteURL)
	statliteMatched := recognizesStatliteMetrics(statliteProbe)
	var statliteCaps []string
	if statliteMatched {
		statliteCaps = statliteCapabilities(statliteProbe.body)
	}
	statliteProbe.body = nil

	quarkusResult, quarkusErr := inspectQuarkusEndpoint(ctx, quarkusURL, i.client.Transport)
	quarkusMatched := quarkusErr == nil

	matchCount := 0
	for _, matched := range []bool{springMatched, statliteMatched, quarkusMatched} {
		if matched {
			matchCount++
		}
	}
	if matchCount > 1 {
		return nil, &Failure{Kind: FailureMultiple, Err: errors.New("more than one supported integration was found")}
	}
	if matchCount == 0 {
		if failure := recognitionFailure(healthProbe, statliteProbe); failure != nil {
			return nil, failure
		}
		if quarkusErr != nil && !isQuarkusConclusiveMiss(quarkusErr) {
			return nil, quarkusErr
		}
	}

	if springMatched {
		result := &Result{TargetType: TargetSpring, Endpoint: actuatorURL, Capabilities: []string{"health"}}
		if i.springMetricsAvailable(ctx, base) {
			result.Capabilities = append(result.Capabilities, "metrics")
		} else {
			result.Warnings = append(result.Warnings, "metrics are unavailable")
		}
		return result, nil
	}

	if statliteMatched {
		result := &Result{
			TargetType:   TargetStatliteMetrics,
			Endpoint:     statliteURL,
			Capabilities: statliteCaps,
		}
		if !containsCapability(statliteCaps, "metrics") {
			result.Warnings = append(result.Warnings, "metrics are unavailable")
		}
		return result, nil
	}
	if quarkusMatched {
		return quarkusResult, nil
	}

	directProbe := i.get(ctx, base.String())
	directMatched := recognizesStatliteMetrics(directProbe)
	var directCapabilities []string
	if directMatched {
		directCapabilities = statliteCapabilities(directProbe.body)
	}
	directProbe.body = nil
	if directMatched {
		result := &Result{
			TargetType:   TargetStatliteMetrics,
			Endpoint:     base.String(),
			Capabilities: directCapabilities,
		}
		if !containsCapability(directCapabilities, "metrics") {
			result.Warnings = append(result.Warnings, "metrics are unavailable")
		}
		return result, nil
	}
	if failure := recognitionFailure(directProbe); failure != nil {
		return nil, failure
	}
	return nil, &Failure{Kind: FailureUnrecognized, Err: errors.New("no supported integration was recognized")}
}

func (i *inspector) springMetricsAvailable(ctx context.Context, base *url.URL) bool {
	client, err := prometheus.NewClientWithTransport(i.timeout, prometheus.DefaultLimits, nil, i.client.Transport)
	if err != nil {
		return false
	}
	compatible, err := collector.InspectSpringPrometheus(ctx, appendPath(base, "actuator/prometheus"), client)
	if compatible {
		return true
	}
	if err != nil && !collector.SpringPrometheusDefinitelyAbsent(err) {
		return false
	}
	return recognizesSpringMetricsIndex(i.get(ctx, appendPath(base, "actuator/metrics")))
}

func isQuarkusMiss(err error) bool {
	var scrapeErr *prometheus.Error
	return errors.As(err, &scrapeErr) && (scrapeErr.Class == prometheus.FailureNotFound || scrapeErr.Class == prometheus.FailureContentType || scrapeErr.Class == prometheus.FailureMalformed)
}

func containsCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func parseApplicationURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, fmt.Errorf("application URL must be a nonblank absolute URL without surrounding whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing application URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("application URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("application URL must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("application URL must not include user information")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, fmt.Errorf("application URL must not include a query")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("application URL must not include a fragment")
	}
	if parsed.Opaque != "" {
		return nil, fmt.Errorf("application URL must use hierarchical URL form")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return parsed, nil
}

func appendPath(base *url.URL, suffix string) string {
	joined := *base
	if joined.Path == "" {
		joined.Path = "/" + suffix
	} else {
		joined.Path = path.Join(joined.Path, suffix)
	}
	joined.RawPath = ""
	return joined.String()
}

func (i *inspector) get(ctx context.Context, endpoint string) probe {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return probe{failure: FailureIncomplete, err: err}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := i.client.Do(req)
	if err != nil {
		kind := FailureUnreachable
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, errRedirectLimit) {
			kind = FailureIncomplete
		}
		return probe{failure: kind, err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return probe{failure: FailureIncomplete, err: err}
	}
	if len(body) > maxResponseBytes {
		return probe{failure: FailureIncomplete, err: fmt.Errorf("response exceeds %d byte limit", maxResponseBytes)}
	}
	kind := FailureKind("")
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		kind = FailureAuthRequired
	}
	return probe{body: body, statusCode: resp.StatusCode, failure: kind}
}

func recognizesSpringHealth(p probe) bool {
	if p.failure != "" || (p.statusCode != http.StatusServiceUnavailable && (p.statusCode < 200 || p.statusCode >= 300)) {
		return false
	}
	var health collector.HealthResponse
	return json.Unmarshal(p.body, &health) == nil && strings.TrimSpace(health.Status) != ""
}

func recognizesSpringMetricsIndex(p probe) bool {
	if p.failure != "" || p.statusCode < 200 || p.statusCode >= 300 {
		return false
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(p.body, &document) != nil {
		return false
	}
	raw, ok := document["names"]
	if !ok {
		return false
	}
	var names []string
	return json.Unmarshal(raw, &names) == nil
}

func recognizesStatliteMetrics(p probe) bool {
	if p.failure != "" || p.statusCode < 200 || p.statusCode >= 300 {
		return false
	}
	_, err := collector.ParseStatliteMetricsResponse(p.body)
	return err == nil
}

func statliteCapabilities(body []byte) []string {
	metrics, err := collector.ParseStatliteMetricsResponse(body)
	if err != nil {
		return nil
	}
	capabilities := []string{"health"}
	if metrics.Metrics != nil {
		capabilities = append(capabilities, "metrics")
	}
	return capabilities
}

func recognitionFailure(probes ...probe) *Failure {
	for _, kind := range []FailureKind{FailureAuthRequired, FailureIncomplete, FailureUnreachable} {
		for _, p := range probes {
			if p.failure == kind {
				return &Failure{Kind: kind, Err: p.err}
			}
		}
	}
	return nil
}
