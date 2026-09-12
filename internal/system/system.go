// Package system exposes basic host health (README §11): CPU, RAM, disk,
// uptime, load. Not a Prometheus/Grafana replacement - just enough for
// `localops system` / GET /system to answer "is the box itself okay".
package system

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

// Snapshot is a point-in-time read of host health.
type Snapshot struct {
	CPUPercent   float64   `json:"cpu_percent"`
	MemPercent   float64   `json:"mem_percent"`
	MemUsedBytes uint64    `json:"mem_used_bytes"`
	MemTotalBytes uint64   `json:"mem_total_bytes"`
	DiskPercent  float64   `json:"disk_percent"`
	DiskUsedBytes uint64   `json:"disk_used_bytes"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	UptimeSeconds uint64   `json:"uptime_seconds"`
	Load1        float64   `json:"load1"`
	Load5        float64   `json:"load5"`
	Load15       float64   `json:"load15"`
	Timestamp    time.Time `json:"timestamp"`
}

// Read gathers a fresh Snapshot. diskPath is the mount point to report on
// (e.g. "/" on Linux, "C:" on Windows).
func Read(ctx context.Context, diskPath string) (Snapshot, error) {
	if diskPath == "" {
		diskPath = "/"
	}

	s := Snapshot{Timestamp: time.Now().UTC()}

	if pct, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false); err == nil && len(pct) > 0 {
		s.CPUPercent = pct[0]
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemPercent = vm.UsedPercent
		s.MemUsedBytes = vm.Used
		s.MemTotalBytes = vm.Total
	}

	if du, err := disk.UsageWithContext(ctx, diskPath); err == nil {
		s.DiskPercent = du.UsedPercent
		s.DiskUsedBytes = du.Used
		s.DiskTotalBytes = du.Total
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		s.UptimeSeconds = info.Uptime
	}

	if avg, err := load.AvgWithContext(ctx); err == nil {
		s.Load1, s.Load5, s.Load15 = avg.Load1, avg.Load5, avg.Load15
	}

	return s, nil
}
