package inspect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
	"github.com/pvrlabs/statlite/internal/urlshape"
)

func inspectMicronaut(parent context.Context, rawURL string) (*Result, error) {
	endpoints, err := micronautInspectionEndpoints(rawURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(parent, defaultTimeout)
	defer cancel()

	var result *Result
	for index, endpoint := range endpoints {
		result, err = inspectMicronautEndpoint(ctx, endpoint, nil)
		if err == nil || index == len(endpoints)-1 || !isMicronautConclusiveMiss(err) {
			return result, err
		}
	}
	return result, err
}

func inspectMicronautEndpoint(ctx context.Context, endpoint string, transport http.RoundTripper) (*Result, error) {
	client, err := prometheus.NewClientWithTransport(defaultTimeout, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	inspection, err := collector.InspectMicronaut(ctx, endpoint, client)
	if err != nil {
		return nil, micronautFailure(err)
	}
	status := CompatibilityCompatible
	if inspection.Status == "partial" {
		status = CompatibilityPartial
	}
	return &Result{
		TargetType:   TargetMicronaut,
		Endpoint:     endpoint,
		Capabilities: inspection.Capabilities,
		Warnings:     inspection.Warnings,
		Status:       status,
	}, nil
}

func micronautInspectionEndpoints(raw string) ([]string, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, fmt.Errorf("Micronaut metrics URL must be a nonblank absolute URL without surrounding whitespace")
	}
	parsed, err := urlshape.ParseHTTP(raw)
	if err != nil {
		return nil, fmt.Errorf("Micronaut metrics URL: %w", err)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("Micronaut metrics URL must not include user information")
	}
	if (parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && !parsed.ForceQuery {
		parsed.Path = "/prometheus"
		parsed.RawPath = ""
		return []string{parsed.String()}, nil
	}
	endpoints := []string{raw}
	if parsed.RawQuery == "" && !parsed.ForceQuery && !strings.HasSuffix(strings.TrimSuffix(parsed.EscapedPath(), "/"), "/prometheus") {
		fallback := *parsed
		escaped := strings.TrimSuffix(parsed.EscapedPath(), "/") + "/prometheus"
		fallback.Path, err = url.PathUnescape(escaped)
		if err != nil {
			return nil, err
		}
		fallback.RawPath = escaped
		endpoints = append(endpoints, fallback.String())
	}
	return endpoints, nil
}

func isMicronautConclusiveMiss(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && (failure.Kind == FailureIncompatible || isMicronautEndpointMiss(failure.Err))
}

func micronautFailure(err error) *Failure {
	if errors.Is(err, collector.ErrMicronautIncompatible) {
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

func isMicronautEndpointMiss(err error) bool {
	var scrapeErr *prometheus.Error
	return errors.As(err, &scrapeErr) && scrapeErr.Class == prometheus.FailureNotFound
}
