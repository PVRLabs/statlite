package config

import (
	"net"
	"strings"
)

// IsLoopbackListen reports whether listen is a loopback-only address.
//
// Safe/local (returns true):
//
//	127.0.0.1:9090
//	localhost:9090
//	[::1]:9090
//
// Non-local (returns false):
//
//	0.0.0.0:9090
//	:9090
//	[::]:9090
//	any other non-loopback host
func IsLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
