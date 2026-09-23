package urlshape

import (
	"strings"
	"testing"
)

func TestParseHTTPDoesNotExposeMalformedInput(t *testing.T) {
	const raw = "http://alice:super-secret@example.com:bad/metrics"
	_, err := ParseHTTP(raw)
	if err == nil {
		t.Fatal("ParseHTTP() error = nil, want malformed URL error")
	}
	if !strings.Contains(err.Error(), "invalid URL syntax") {
		t.Fatalf("ParseHTTP() error = %q, want useful syntax diagnosis", err)
	}
	if strings.Contains(err.Error(), "super-secret") || strings.Contains(err.Error(), raw) {
		t.Fatalf("ParseHTTP() error = %q, must not expose credentials or raw URL", err)
	}
}

func TestParseHTTPRejectsLiteralEmptyFragmentButAllowsEscapedHash(t *testing.T) {
	for _, raw := range []string{"http://example.com/metrics#", "http://example.com/metrics#part"} {
		if _, err := ParseHTTP(raw); err == nil || !strings.Contains(err.Error(), "fragment") {
			t.Errorf("ParseHTTP(%q) error = %v, want fragment error", raw, err)
		}
	}
	if _, err := ParseHTTP("http://example.com/metrics%23suffix"); err != nil {
		t.Fatalf("ParseHTTP() error = %v, want percent-encoded # accepted", err)
	}
}
