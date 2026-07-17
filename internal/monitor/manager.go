package monitor

// This file coordinates monitor instances across configured targets.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/storage"
)

type TargetMetadata struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Endpoint       string `json:"endpoint"`
	EndpointSource string `json:"endpoint_source"`
}

type ManagedTarget struct {
	Metadata TargetMetadata
	Monitor  *Monitor
}

type TargetSummary struct {
	Metadata TargetMetadata    `json:"metadata"`
	Status   Status            `json:"status"`
	Latest   *storage.Snapshot `json:"latest,omitempty"`
}

type Manager struct {
	order   []string
	targets map[string]ManagedTarget
}

func NewManager(targets []ManagedTarget) (*Manager, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("at least one monitor target is required")
	}

	manager := &Manager{
		order:   make([]string, 0, len(targets)),
		targets: make(map[string]ManagedTarget, len(targets)),
	}
	for i, target := range targets {
		name := strings.TrimSpace(target.Metadata.Name)
		if name == "" {
			return nil, fmt.Errorf("targets[%d].name is required", i)
		}
		if target.Monitor == nil {
			return nil, fmt.Errorf("targets[%d].monitor is required", i)
		}
		if _, ok := manager.targets[name]; ok {
			return nil, fmt.Errorf("targets[%d].name %q is duplicated", i, name)
		}
		target.Metadata.Name = name
		manager.order = append(manager.order, name)
		manager.targets[name] = target
	}
	return manager, nil
}

func (m *Manager) Start(ctx context.Context) {
	for _, name := range m.order {
		m.targets[name].Monitor.Start(ctx)
	}
}

func (m *Manager) Names() []string {
	names := make([]string, len(m.order))
	copy(names, m.order)
	return names
}

func (m *Manager) Summaries() []TargetSummary {
	summaries := make([]TargetSummary, 0, len(m.order))
	for _, name := range m.order {
		target := m.targets[name]
		summaries = append(summaries, TargetSummary{
			Metadata: target.Metadata,
			Status:   target.Monitor.Status(),
			Latest:   target.Monitor.LatestSnapshot(),
		})
	}
	return summaries
}

func (m *Manager) ResolveTarget(name string) ManagedTarget {
	if target, ok := m.targets[strings.TrimSpace(name)]; ok {
		return target
	}
	for _, targetName := range m.order {
		target := m.targets[targetName]
		if target.Monitor.Status().ConsecutivePollFailures > 0 {
			return target
		}
	}
	for _, targetName := range m.order {
		target := m.targets[targetName]
		if snapshotUnhealthy(target.Monitor.LatestSnapshot()) {
			return target
		}
	}
	return m.targets[m.order[0]]
}

func (m *Manager) Monitor(name string) *Monitor {
	return m.ResolveTarget(name).Monitor
}

func (m *Manager) Metadata(name string) TargetMetadata {
	return m.ResolveTarget(name).Metadata
}

func (m *Manager) Status(name string) Status {
	return m.ResolveTarget(name).Monitor.Status()
}

func (m *Manager) LatestSnapshot(name string) *storage.Snapshot {
	return m.ResolveTarget(name).Monitor.LatestSnapshot()
}

func (m *Manager) PollNow(ctx context.Context, name string) (*storage.Snapshot, error) {
	return m.ResolveTarget(name).Monitor.PollNow(ctx)
}

func (m *Manager) Series(ctx context.Context, name string, start, end time.Time) (*storage.Series, error) {
	return m.ResolveTarget(name).Monitor.Series(ctx, start, end)
}

func snapshotUnhealthy(snapshot *storage.Snapshot) bool {
	if snapshot == nil {
		return false
	}
	return statusUnhealthy(snapshot.Result.HealthStatus) || statusUnhealthy(snapshot.Result.DBHealthStatus)
}

func statusUnhealthy(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "DOWN", "ERROR":
		return true
	default:
		return false
	}
}
