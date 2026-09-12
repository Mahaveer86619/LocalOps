// Package health runs the periodic sweep that turns "no heartbeat in a
// while" into a Slack alert (README §8: a stuck task is a good candidate
// to raise through the same alerting path as watcher failures), and backs
// the plain GET /health liveness endpoint.
package health

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Mahaveer86619/LocalOps/internal/notify"
	"github.com/Mahaveer86619/LocalOps/internal/tasks"
)

// Monitor periodically scans running tasks for staleness and alerts once
// per stuck episode (not once per sweep tick).
type Monitor struct {
	Tasks           *tasks.Store
	Notifier        *notify.Slack
	StuckMultiplier int
	SweepInterval   time.Duration

	mu      sync.Mutex
	alerted map[string]bool // task IDs already alerted for the current stuck episode
}

func NewMonitor(store *tasks.Store, notifier *notify.Slack, stuckMultiplier int, sweepInterval time.Duration) *Monitor {
	if stuckMultiplier <= 0 {
		stuckMultiplier = 3
	}
	if sweepInterval <= 0 {
		sweepInterval = 30 * time.Second
	}
	return &Monitor{
		Tasks:           store,
		Notifier:        notifier,
		StuckMultiplier: stuckMultiplier,
		SweepInterval:   sweepInterval,
		alerted:         make(map[string]bool),
	}
}

// Run blocks, sweeping on SweepInterval until ctx is cancelled. Intended
// to be launched with `go monitor.Run(ctx)` from main.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep(ctx)
		}
	}
}

func (m *Monitor) sweep(ctx context.Context) {
	running, err := m.Tasks.Running(ctx)
	if err != nil {
		log.Printf("health: sweep: list running tasks: %v", err)
		return
	}

	now := time.Now().UTC()
	stillStuck := make(map[string]bool, len(running))

	for _, t := range running {
		if !t.Stuck(now, m.StuckMultiplier) {
			continue
		}
		stillStuck[t.ID] = true

		m.mu.Lock()
		already := m.alerted[t.ID]
		m.alerted[t.ID] = true
		m.mu.Unlock()

		if already {
			continue // already alerted for this episode - a heartbeat/completion resets it
		}

		last := t.StartedAt
		if t.LastHeartbeatAt != nil {
			last = t.LastHeartbeatAt
		}
		minutes := 0
		if last != nil {
			minutes = int(now.Sub(*last).Minutes())
		}
		m.Notifier.TaskStuck(ctx, t.ID, fmt.Sprintf("%s/%s: %s", t.Server, t.Type, t.Description), minutes)
	}

	// Clear alert state for anything no longer stuck (heartbeat resumed,
	// or the task finished) so a future stuck episode alerts again.
	m.mu.Lock()
	for id := range m.alerted {
		if !stillStuck[id] {
			delete(m.alerted, id)
		}
	}
	m.mu.Unlock()
}
