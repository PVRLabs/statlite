package inspect

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

func inspectQuarkus(parent context.Context, rawURL string) (*Result, error) {
	endpoint, err := parseQuarkusEndpoint(rawURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(parent, defaultTimeout)
	defer cancel()

	client, err := prometheus.NewClient(defaultTimeout, prometheus.DefaultLimits, nil)
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
	if strings.TrimSpace(raw) != raw || raw == "" {
		return "", fmt.Errorf("Quarkus metrics URL must be a nonblank absolute URL without surrounding whitespace")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parsing Quarkus metrics URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("Quarkus metrics URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("Quarkus metrics URL must include a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("Quarkus metrics URL must not include user information")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("Quarkus metrics URL must not include a fragment")
	}
	if parsed.Opaque != "" {
		return "", fmt.Errorf("Quarkus metrics URL must use hierarchical URL form")
	}
	return parsed.String(), nil
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
