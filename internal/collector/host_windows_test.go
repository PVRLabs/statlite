package collector

import "testing"

func TestWindowsHostMetricsRemainUnavailable(t *testing.T) {
	metrics, warnings := NewHostSampler().Sample(t.TempDir())
	if metrics.CPUUsage != nil || metrics.MemoryUsedBytes != nil || metrics.MemoryTotalBytes != nil || metrics.DiskUsedBytes != nil || metrics.DiskTotalBytes != nil {
		t.Fatalf("unsupported host metrics must be absent: %+v", metrics)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v, want three unsupported resource warnings", warnings)
	}
}
