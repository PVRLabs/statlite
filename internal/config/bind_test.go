package config

import "testing"

func TestIsLoopbackListen(t *testing.T) {
	tests := []struct {
		listen string
		want   bool
	}{
		// Safe / local
		{"127.0.0.1:9090", true},
		{"localhost:9090", true},
		{"[::1]:9090", true},
		{"127.0.0.1:0", true},
		{"localhost:8080", true},
		{"[::1]:80", true},

		// Non-local
		{"0.0.0.0:9090", false},
		{":9090", false},
		{"[::]:9090", false},
		{"192.168.1.1:9090", false},
		{"10.0.0.1:9090", false},
		{"example.com:9090", false},

		// Malformed
		{"", false},
		{"not-a-valid-address", false},
		{"127.0.0.1", false},
	}

	for _, tt := range tests {
		got := IsLoopbackListen(tt.listen)
		if got != tt.want {
			t.Errorf("IsLoopbackListen(%q) = %v, want %v", tt.listen, got, tt.want)
		}
	}
}
