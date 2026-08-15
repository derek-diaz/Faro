package protectiontime

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule describes the local recurring window in which a protection setup
// is active. Days use ISO weekday numbers (Monday=1, Sunday=7).
type Schedule struct {
	Enabled  bool
	Days     string
	Start    string
	End      string
	Timezone string
}

func Validate(schedule Schedule) (Schedule, error) {
	schedule.Days = normalizeDays(schedule.Days)
	schedule.Start = strings.TrimSpace(schedule.Start)
	schedule.End = strings.TrimSpace(schedule.End)
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	if schedule.Timezone == "" {
		schedule.Timezone = "UTC"
	}
	if !schedule.Enabled {
		return schedule, nil
	}
	if schedule.Days == "" {
		return schedule, fmt.Errorf("choose at least one schedule day")
	}
	if _, err := clockMinutes(schedule.Start); err != nil {
		return schedule, fmt.Errorf("invalid schedule start: %w", err)
	}
	if _, err := clockMinutes(schedule.End); err != nil {
		return schedule, fmt.Errorf("invalid schedule end: %w", err)
	}
	if schedule.Start == schedule.End {
		return schedule, fmt.Errorf("schedule start and end must be different")
	}
	if _, err := time.LoadLocation(schedule.Timezone); err != nil {
		return schedule, fmt.Errorf("unknown schedule timezone")
	}
	return schedule, nil
}

func ActiveAt(schedule Schedule, now time.Time) bool {
	if !schedule.Enabled {
		return true
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return false
	}
	local := now.In(location)
	start, startErr := clockMinutes(schedule.Start)
	end, endErr := clockMinutes(schedule.End)
	if startErr != nil || endErr != nil {
		return false
	}
	minute := local.Hour()*60 + local.Minute()
	today := isoWeekday(local.Weekday())
	if start < end {
		return containsDay(schedule.Days, today) && minute >= start && minute < end
	}
	// Overnight windows belong to the day on which they start.
	if minute >= start {
		return containsDay(schedule.Days, today)
	}
	previous := today - 1
	if previous == 0 {
		previous = 7
	}
	return minute < end && containsDay(schedule.Days, previous)
}

func PausedAt(pausedUntil string, now time.Time) bool {
	pausedUntil = strings.TrimSpace(pausedUntil)
	if pausedUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, pausedUntil)
	return err == nil && now.Before(until)
}

func normalizeDays(value string) string {
	seen := map[int]bool{}
	for _, raw := range strings.Split(value, ",") {
		day, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && day >= 1 && day <= 7 {
			seen[day] = true
		}
	}
	days := make([]string, 0, len(seen))
	for day := 1; day <= 7; day++ {
		if seen[day] {
			days = append(days, strconv.Itoa(day))
		}
	}
	return strings.Join(days, ",")
}

func clockMinutes(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("use HH:MM")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func containsDay(days string, day int) bool {
	needle := strconv.Itoa(day)
	for _, value := range strings.Split(days, ",") {
		if strings.TrimSpace(value) == needle {
			return true
		}
	}
	return false
}

func isoWeekday(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}
