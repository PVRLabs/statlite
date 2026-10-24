//go:build darwin

package collector

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

const (
	darwinHostVMInfo      = 2
	darwinHostCPULoadInfo = 3
	darwinKernelSuccess   = 0
)

type darwinCPULoadInfo struct {
	User   uint32
	System uint32
	Idle   uint32
	Nice   uint32
}

type darwinVMStatistics struct {
	Free        uint32
	Active      uint32
	Inactive    uint32
	Wired       uint32
	ZeroFilled  uint32
	Reactivated uint32
	Pageins     uint32
	Pageouts    uint32
	Faults      uint32
	CopyOnWrite uint32
	Lookups     uint32
	Hits        uint32
	Purgeable   uint32
	Purges      uint32
	Speculative uint32
}

var darwinSystem = struct {
	once           sync.Once
	err            error
	machHostSelf   func() uint32
	hostStatistics func(uint32, int32, unsafe.Pointer, *uint32) int32
}{}

func (s *HostSampler) cpuUsage() (*float64, error) {
	if err := loadDarwinSystem(); err != nil {
		return nil, err
	}
	var load darwinCPULoadInfo
	count := uint32(unsafe.Sizeof(load) / unsafe.Sizeof(uint32(0)))
	status := darwinSystem.hostStatistics(
		darwinSystem.machHostSelf(),
		darwinHostCPULoadInfo,
		unsafe.Pointer(&load),
		&count,
	)
	if status != darwinKernelSuccess {
		return nil, fmt.Errorf("host_statistics CPU status %d", status)
	}
	total, idle := darwinCPUTimes(load)
	return s.cpuUsageFromCounters(total, idle)
}

func loadDarwinSystem() error {
	darwinSystem.once.Do(func() {
		handle, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_LOCAL)
		if err != nil {
			darwinSystem.err = fmt.Errorf("load libSystem: %w", err)
			return
		}
		purego.RegisterLibFunc(&darwinSystem.machHostSelf, handle, "mach_host_self")
		purego.RegisterLibFunc(&darwinSystem.hostStatistics, handle, "host_statistics")
	})
	return darwinSystem.err
}

func darwinCPUTimes(load darwinCPULoadInfo) (uint64, uint64) {
	total := uint64(load.User) + uint64(load.System) + uint64(load.Idle) + uint64(load.Nice)
	return total, uint64(load.Idle)
}

func hostMemory() (float64, float64, error) {
	if err := loadDarwinSystem(); err != nil {
		return 0, 0, err
	}
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, 0, fmt.Errorf("read hw.memsize: %w", err)
	}
	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return 0, 0, fmt.Errorf("read hw.pagesize: %w", err)
	}

	var stats darwinVMStatistics
	count := uint32(unsafe.Sizeof(stats) / unsafe.Sizeof(uint32(0)))
	status := darwinSystem.hostStatistics(
		darwinSystem.machHostSelf(),
		darwinHostVMInfo,
		unsafe.Pointer(&stats),
		&count,
	)
	if status != darwinKernelSuccess {
		return 0, 0, fmt.Errorf("host_statistics memory status %d", status)
	}
	available := darwinAvailableMemory(stats, uint64(pageSize))
	if total == 0 || available > total {
		return 0, 0, fmt.Errorf("invalid memory totals")
	}
	return float64(total - available), float64(total), nil
}

func darwinAvailableMemory(stats darwinVMStatistics, pageSize uint64) uint64 {
	return (uint64(stats.Free) + uint64(stats.Inactive) + uint64(stats.Speculative)) * pageSize
}
