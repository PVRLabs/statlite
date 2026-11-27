package config

import (
	"strings"
	"testing"
)

func TestTraefikConfiguration(t *testing.T) {
	const target = `  - name: edge
    type: traefik
    url: http://localhost:8082/metrics
`
	const service = "    service: alpha@file\n"
	for _, tc := range []struct{ name, targets, wantError string }{
		{"missing", target, "service:"},
		{"empty", target + "    service: ''\n", "service:"},
		{"unqualified", target + "    service: alpha\n", "provider-qualified"},
		{"multiple separators", target + "    service: alpha@extra@file\n", "provider-qualified"},
		{"invalid provider", target + "    service: alpha@\n", "provider-qualified"},
		{"unsupported target", strings.Replace(target, "type: traefik", "type: quarkus", 1) + service, "service: is supported only for type traefik"},
		{"unsupported null selector", strings.Replace(target, "type: traefik", "type: spring", 1) + "    service: null\n", "service: is supported only for type traefik"},
		{"duplicate", target + service + strings.Replace(target, "name: edge", "name: other", 1) + service, "duplicates Traefik endpoint and service"},
		{"distinct services", target + service + strings.Replace(target, "name: edge", "name: other", 1) + "    service: beta+blue@file\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, caddyConfigPrefix+tc.targets))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got %v, want error containing %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Targets) != 2 || cfg.Targets[0].Service != "alpha@file" || cfg.Targets[1].Service != "beta+blue@file" ||
				cfg.Targets[0].URL != cfg.Targets[1].URL {
				t.Fatalf("targets = %+v", cfg.Targets)
			}
		})
	}
}
