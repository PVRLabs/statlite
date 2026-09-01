package inspect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

func inspectQuarkus(parent context.Context, rawURL string) (*Result, error) {
	endpoints, err := quarkusInspectionEndpoints(rawURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(parent, defaultTimeout)
	defer cancel()

	var result *Result
	for index, endpoint := range endpoints {
		result, err = inspectQuarkusEndpoint(ctx, endpoint, nil)
		if err == nil || index == len(endpoints)-1 || !isQuarkusConclusiveMiss(err) {
			return result, err
		}
	}
	return result, err
}

func inspectQuarkusEndpoint(ctx context.Context, endpoint string, transport http.RoundTripper) (*Result, error) {
	client, err := prometheus.NewClientWithTransport(defaultTimeout, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	inspection, err := collector.InspectQuarkus(ctx, endpoint, client)
	if err != nil {
		return nil, quarkusFailure(err)
	}
	status := CompatibilityCompatible
	if inspection.Status == "partial" {
		status = CompatibilityPartial
	}
	return &Result{
		TargetType:   TargetQuarkus,
		Endpoint:     endpoint,
		Capabilities: inspection.Capabilities,
		Warnings:     inspection.Warnings,
		Status:       status,
	}, nil
}

func parseQuarkusEndpoint(raw string) (string, error) {
	endpoints, err := quarkusInspectionEndpoints(raw)
	if err != nil {
		return "", err
	}
	return endpoints[0], nil
}

func quarkusInspectionEndpoints(raw string) ([]string, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, fmt.Errorf("Quarkus metrics URL must be a nonblank absolute URL without surrounding whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing Quarkus metrics URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("Quarkus metrics URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("Quarkus metrics URL must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("Quarkus metrics URL must not include user information")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("Quarkus metrics URL must not include a fragment")
	}
	if parsed.Opaque != "" {
		return nil, fmt.Errorf("Quarkus metrics URL must use hierarchical URL form")
	}
	if (parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && !parsed.ForceQuery {
		parsed.Path = path.Join(parsed.Path, "/q/metrics")
		parsed.RawPath = ""
		return []string{parsed.String()}, nil
	}
	endpoints := []string{parsed.String()}
	if parsed.RawQuery == "" && !parsed.ForceQuery && !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/q/metrics") {
		fallback := *parsed
		fallback.Path = path.Join(fallback.Path, "q/metrics")
		fallback.RawPath = ""
		endpoints = append(endpoints, fallback.String())
	}
	return endpoints, nil
}

func isQuarkusConclusiveMiss(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && (failure.Kind == FailureIncompatible || isQuarkusMiss(failure.Err))
}

func quarkusFailure(err error) *Failure {
	if errors.Is(err, collector.ErrQuarkusIncompatible) {
		return &Failure{Kind: FailureIncompatible, Err: err}
	}
	var scrapeErr *prometheus.Error
	if errors.As(err, &scrapeErr) {
		switch scrapeErr.Class {
		case prometheus.FailureAuthentication:
			return &Failure{Kind: FailureAuthRequired, Err: err}
		case prometheus.FailureTransport:
			if errors.Is(err, context.DeadlineExceeded) {
				return &Failure{Kind: FailureIncomplete, Err: err}
			}
			return &Failure{Kind: FailureUnreachable, Err: err}
		case prometheus.FailureInvalidURL:
			return &Failure{Kind: FailureIncomplete, Err: err}
		default:
			return &Failure{Kind: FailureIncomplete, Err: err}
		}
	}
	return &Failure{Kind: FailureIncomplete, Err: err}
}
