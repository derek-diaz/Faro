package db

import (
	"errors"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

const (
	ActivityStorageHealthy  = "healthy"
	ActivityStoragePaused   = "paused"
	ActivityStorageDegraded = "degraded"
	activityRetryDelay      = 30 * time.Second
)

// ActivityStorageStatus describes the best-effort history path separately from
// the control plane. A full disk must not make DNS look unhealthy.
type ActivityStorageStatus struct {
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	LastFailureAt string `json:"last_failure_at,omitempty"`
}

type activityStorageState struct {
	ActivityStorageStatus
	retryAt time.Time
}

func (store *Store) ActivityStorageStatus() ActivityStorageStatus {
	store.activityMu.RLock()
	defer store.activityMu.RUnlock()
	status := store.activity.ActivityStorageStatus
	if status.Status == "" {
		status.Status = ActivityStorageHealthy
	}
	return status
}

// ActivityStorageWriteAllowed throttles repeated writes after a durable
// storage failure. The next retry is allowed after a cooldown so freeing disk
// space lets the logger recover automatically without restarting Faro.
func (store *Store) ActivityStorageWriteAllowed() bool {
	store.activityMu.RLock()
	defer store.activityMu.RUnlock()
	return store.activity.Status == "" || store.activity.Status == ActivityStorageHealthy || time.Now().After(store.activity.retryAt)
}

func (store *Store) ReportActivityWriteFailure(err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	status := ActivityStorageDegraded
	reason := "Database write failed"
	delay := 5 * time.Second
	if isDiskFullError(err) {
		status = ActivityStoragePaused
		reason = "Insufficient disk space"
		delay = activityRetryDelay
	}

	store.activityMu.Lock()
	store.activity.ActivityStorageStatus = ActivityStorageStatus{
		Status:        status,
		Reason:        reason,
		LastError:     truncateActivityError(err.Error()),
		LastFailureAt: now.Format(time.RFC3339),
	}
	store.activity.retryAt = now.Add(delay)
	store.activityMu.Unlock()
}

func (store *Store) ReportActivityWriteSuccess() {
	store.activityMu.Lock()
	store.activity.ActivityStorageStatus = ActivityStorageStatus{Status: ActivityStorageHealthy}
	store.activity.retryAt = time.Time{}
	store.activityMu.Unlock()
}

func isDiskFullError(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		if sqliteErr.Code == sqlite3.ErrFull {
			return true
		}
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "disk is full") ||
		strings.Contains(message, "no space left on device") ||
		strings.Contains(message, "not enough space")
}

func truncateActivityError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 500 {
		return value
	}
	return value[:500]
}
