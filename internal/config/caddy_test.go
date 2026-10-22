package config

import (
	"strings"
	"testing"
)

const caddyConfigPrefix = `server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
`
const caddyTargetYAML = `  - name: app
    type: caddy
    url: http://localhost:2019/metrics
    host: alpha.test
`

func TestCaddyConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, target, want string }{
		{"ordinary", caddyTargetYAML, ""},
		{"basic auth", caddyTargetYAML + "    auth: {type: basic, username: user, password: secret}\n", ""},
		{"two hosts", caddyTargetYAML + strings.ReplaceAll(strings.ReplaceAll(caddyTargetYAML, "name: app", "name: second"), "alpha.test", "beta.test"), ""},
		{"duplicate", caddyTargetYAML + strings.ReplaceAll(caddyTargetYAML, "name: app", "name: second"), "duplicates Caddy"},
		{"normalized duplicate", strings.ReplaceAll(caddyTargetYAML, "localhost:2019/metrics", "LOCALHOST:80/") + strings.ReplaceAll(strings.ReplaceAll(caddyTargetYAML, "name: app", "name: second"), "localhost:2019/metrics", "localhost"), "duplicates Caddy"},
		{"absent host", strings.ReplaceAll(caddyTargetYAML, "    host: alpha.test\n", ""), "host:"},
		{"empty host", strings.ReplaceAll(caddyTargetYAML, "alpha.test", `""`), "host:"},
		{"null host", strings.ReplaceAll(caddyTargetYAML, "alpha.test", "null"), "host:"},
		{"absent url", strings.ReplaceAll(caddyTargetYAML, "    url: http://localhost:2019/metrics\n", ""), "url:"},
		{"userinfo", strings.ReplaceAll(caddyTargetYAML, "http://localhost", "http://user:secret@localhost"), "credentials"},
		{"fragment", strings.ReplaceAll(caddyTargetYAML, "/metrics", "/metrics#part"), "fragment"},
		{"invalid auth", caddyTargetYAML + "    auth: {type: token, username: user, password: secret}\n", "auth.type"},
		{"missing password", caddyTargetYAML + "    auth: {type: basic, username: user}\n", "auth.password"},
		{"colon username", caddyTargetYAML + "    auth: {type: basic, username: 'u:s', password: secret}\n", "auth.username"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, caddyConfigPrefix+tc.target))
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error %v want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Targets[0].Host != "alpha.test" || cfg.Targets[0].Type != TargetTypeCaddy || cfg.Targets[0].DisplayMetadata().EndpointSource != "url" {
				t.Fatalf("target: %+v", cfg.Targets[0])
			}
		})
	}
}
func TestCaddyIncompatibleFieldPresence(t *testing.T) {
	for _, field := range []string{"actuator_base_url", "metrics_source", "collect_host_metrics", "health_url"} {
		for _, value := range []string{"null", `""`} {
			if field == "collect_host_metrics" && value == `""` {
				value = "false"
			}
			t.Run(field+value, func(t *testing.T) {
				_, err := Load(writeConfig(t, caddyConfigPrefix+caddyTargetYAML+"    "+field+": "+value+"\n"))
				if err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("error: %v", err)
				}
			})
		}
	}
	for _, typ := range []string{TargetTypeSpring, TargetTypeQuarkus, TargetTypeMicronaut, TargetTypeStatliteMetrics} {
		for _, value := range []string{"null", `""`, "alpha.test"} {
			t.Run(typ+value, func(t *testing.T) {
				target := strings.ReplaceAll(strings.ReplaceAll(caddyTargetYAML, "type: caddy", "type: "+typ), "alpha.test", value)
				_, err := Load(writeConfig(t, caddyConfigPrefix+target))
				if err == nil || !strings.Contains(err.Error(), "host: is supported only") {
					t.Fatalf("error: %v", err)
				}
			})
		}
	}
}
func TestCaddyExactHostValidation(t *testing.T) {
	for _, host := range []string{"*.test", "https://alpha.test", "alpha.test/path", "alpha.test?x", " alpha.test", "alpha.test ", "_other", "-alpha.test", "alpha..test", "alpha.test:", "alpha.test:0", "alpha.test:65536", "alpha.test:abc"} {
		t.Run(host, func(t *testing.T) {
			cfg := validConfig(TargetTypeCaddy, "http://localhost/metrics")
			cfg.Targets[0].Host = host
			if err := Validate(cfg); err == nil {
				t.Fatal("accepted invalid host")
			}
		})
	}
	for _, host := range []string{"alpha.test", "Alpha.test", "alpha.test:8443", "localhost"} {
		t.Run(host, func(t *testing.T) {
			cfg := validConfig(TargetTypeCaddy, "http://localhost/metrics")
			cfg.Targets[0].Host = host
			if err := Validate(cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Targets[0].Host != host {
				t.Fatal("exact host changed")
			}
		})
	}
	for _, field := range []string{"metrics_source", "actuator_base_url", "health_url", "collect_host_metrics"} {
		t.Run("programmatic "+field, func(t *testing.T) {
			cfg := validConfig(TargetTypeCaddy, "http://localhost/metrics")
			target := &cfg.Targets[0]
			target.Host = "alpha.test"
			switch field {
			case "metrics_source":
				target.MetricsSource = "auto"
			case "actuator_base_url":
				target.ActuatorBaseURL = "http://localhost"
			case "health_url":
				target.HealthURL = "http://localhost/health"
			case "collect_host_metrics":
				target.CollectHostMetrics = true
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("error %v", err)
			}
		})
	}
}
