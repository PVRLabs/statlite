package collector

// This file collects lightweight, best-effort local host resource estimates.

import (
	"fmt"
	"sync"
)

type HostSampler struct {
	mu        sync.Mutex
	lastTotal uint64
	lastIdle  uint64
	haveCPU   bool
}

type HostMetrics struct {
	CPUUsage         *float64
	MemoryUsedBytes  *float64
	MemoryTotalBytes *float64
	DiskUsedBytes    *float64
	DiskTotalBytes   *float64
}

func NewHostSampler() *HostSampler { return &HostSampler{} }

func cpuUsageForInterval(totalDelta, idleDelta uint64) (*float64, error) {
	if totalDelta == 0 {
		if idleDelta != 0 {
			return nil, fmt.Errorf("invalid CPU interval")
		}
		// CPU counters advance in clock ticks. Reads can happen before the next
		// tick, especially in tests or with a short polling interval.
		return nil, nil
	}
	if idleDelta > totalDelta {
		return nil, fmt.Errorf("invalid CPU interval")
	}
	usage := float64(totalDelta-idleDelta) / float64(totalDelta)
	return &usage, nil
}

func (s *HostSampler) cpuUsageFromCounters(total, idle uint64) (*float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.haveCPU || total < s.lastTotal || idle < s.lastIdle {
		s.lastTotal, s.lastIdle, s.haveCPU = total, idle, true
		return nil, nil
	}
	totalDelta, idleDelta := total-s.lastTotal, idle-s.lastIdle
	s.lastTotal, s.lastIdle = total, idle
	return cpuUsageForInterval(totalDelta, idleDelta)
}

func (s *HostSampler) Sample(filesystemPath string) (HostMetrics, []error) {
	var metrics HostMetrics
	var warnings []error
	if value, err := s.cpuUsage(); err != nil {
		warnings = append(warnings, fmt.Errorf("host CPU usage unavailable: %w", err))
	} else {
		metrics.CPUUsage = value
	}
	if used, total, err := hostMemory(); err != nil {
		warnings = append(warnings, fmt.Errorf("host memory unavailable: %w", err))
	} else {
		metrics.MemoryUsedBytes = &used
		metrics.MemoryTotalBytes = &total
	}
	if used, total, err := diskUsage(filesystemPath); err != nil {
		warnings = append(warnings, fmt.Errorf("host disk usage unavailable: %w", err))
	} else {
		metrics.DiskUsedBytes = &used
		metrics.DiskTotalBytes = &total
	}
	return metrics, warnings
}
