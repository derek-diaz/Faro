package retention

import (
	"context"
	"log"
	"time"

	"github.com/derek/faro/internal/db"
)

type Manager struct {
	Store    *db.Store
	Interval time.Duration
}

func NewManager(store *db.Store) *Manager {
	return &Manager{Store: store, Interval: 6 * time.Hour}
}

func (m *Manager) Run(ctx context.Context) {
	m.prune(ctx)
	interval := m.Interval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.prune(ctx)
		}
	}
}

func (m *Manager) prune(ctx context.Context) {
	days := ConfiguredDays(ctx, m.Store)
	result, err := Prune(ctx, m.Store, days, false)
	if err != nil {
		log.Printf("retention prune failed: %v", err)
		return
	}
	if result.QueriesDeleted > 0 || result.EventsDeleted > 0 {
		log.Printf("retention pruned %d DNS queries and %d events older than %d days", result.QueriesDeleted, result.EventsDeleted, days)
	}
}
