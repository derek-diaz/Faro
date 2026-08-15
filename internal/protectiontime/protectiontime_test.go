package protectiontime

import (
	"testing"
	"time"
)

func TestActiveAtSupportsDaytimeAndOvernightWindows(t *testing.T) {
	daytime := Schedule{Enabled: true, Days: "1,2,3,4,5", Start: "08:00", End: "20:00", Timezone: "UTC"}
	if !ActiveAt(daytime, time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("weekday daytime window should be active")
	}
	if ActiveAt(daytime, time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("Sunday should be outside weekday window")
	}
	overnight := Schedule{Enabled: true, Days: "5", Start: "22:00", End: "07:00", Timezone: "UTC"}
	if !ActiveAt(overnight, time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC)) {
		t.Fatal("early Saturday should remain in Friday's overnight window")
	}
}

func TestValidateNormalizesDays(t *testing.T) {
	schedule, err := Validate(Schedule{Enabled: true, Days: "7,1,1", Start: "09:00", End: "17:00", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Days != "1,7" {
		t.Fatalf("days = %q; want 1,7", schedule.Days)
	}
}
