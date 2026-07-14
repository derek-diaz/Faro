package handlers

import (
	"testing"
	"time"
)

func TestLocalDayStartUsesBrowserTimezone(t *testing.T) {
	now := time.Date(2026, time.July, 13, 0, 20, 0, 0, time.UTC)
	got := localDayStart(now, "America/Puerto_Rico")
	want := "2026-07-12T04:00:00Z"
	if got != want {
		t.Fatalf("local day started at %s, want %s", got, want)
	}
}

func TestLocalDayStartFallsBackToUTC(t *testing.T) {
	now := time.Date(2026, time.July, 13, 0, 20, 0, 0, time.UTC)
	got := localDayStart(now, "not-a-timezone")
	want := "2026-07-13T00:00:00Z"
	if got != want {
		t.Fatalf("UTC day started at %s, want %s", got, want)
	}
}
