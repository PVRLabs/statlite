//go:build linux

package collector

import (
	"strings"
	"testing"
)

func TestParseCPUStatExcludesGuestCounters(t *testing.T) {
	total, idle, err := parseCPUStat("cpu 100 20 30 400 10 5 5 5 500 500")
	if err != nil {
		t.Fatalf("parseCPUStat() error = %v", err)
	}
	if total != 575 || idle != 410 {
		t.Fatalf("parseCPUStat() = total %d idle %d, want total 575 idle 410", total, idle)
	}
}

func TestParseHostMemoryRequiresMemAvailable(t *testing.T) {
	for _, input := range []string{
		"MemTotal:       100 kB\n",
		"MemTotal:       100 kB\nMemAvailable: invalid kB\n",
	} {
		if _, _, err := parseHostMemory(strings.NewReader(input)); err == nil {
			t.Fatalf("parseHostMemory(%q) error = nil, want unavailable memory", input)
		}
	}
	used, total, err := parseHostMemory(strings.NewReader("MemTotal:       100 kB\nMemAvailable: 40 kB\n"))
	if err != nil || used != 60*1024 || total != 100*1024 {
		t.Fatalf("parseHostMemory() = used %v total %v err %v, want 61440 102400 nil", used, total, err)
	}
}
