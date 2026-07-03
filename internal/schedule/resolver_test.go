package schedule

import (
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

func TestEvaluateWindows_SimpleWeekday(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 17 * 60}, // Mon 09:00-17:00
	}
	loc := time.UTC
	cases := []struct {
		when string
		want bool
	}{
		{"2026-05-04T09:00:00Z", true},  // Mon 09:00 -> true
		{"2026-05-04T08:59:00Z", false}, // Mon 08:59 -> false
		{"2026-05-04T17:00:00Z", false}, // Mon 17:00 -> false (exclusive)
		{"2026-05-05T10:00:00Z", false}, // Tue 10:00 -> false (no schedule)
	}
	for _, tc := range cases {
		ts, _ := time.ParseInLocation(time.RFC3339, tc.when, loc)
		on, _ := evaluateWindows(schedules, ts)
		if on != tc.want {
			t.Errorf("%s: got %v want %v", tc.when, on, tc.want)
		}
	}
}

func TestEvaluateWindows_WrapMidnight(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 5, StartMinute: 22 * 60, EndMinute: 2 * 60}, // Fri 22:00 -> Sat 02:00
	}
	loc := time.UTC
	cases := []struct {
		when string
		want bool
	}{
		{"2026-05-08T23:00:00Z", true},  // Fri 23:00 -> true (start side)
		{"2026-05-09T01:30:00Z", true},  // Sat 01:30 -> true (wrap)
		{"2026-05-09T02:00:00Z", false}, // Sat 02:00 -> false (exclusive)
		{"2026-05-09T03:00:00Z", false},
	}
	for _, tc := range cases {
		ts, _ := time.ParseInLocation(time.RFC3339, tc.when, loc)
		on, _ := evaluateWindows(schedules, ts)
		if on != tc.want {
			t.Errorf("%s: got %v want %v", tc.when, on, tc.want)
		}
	}
}

func TestEvaluateWindows_UnionOverlapping(t *testing.T) {
	schedules := []*store.RecordingSchedule{
		{DayOfWeek: 3, StartMinute: 9 * 60, EndMinute: 12 * 60},
		{DayOfWeek: 3, StartMinute: 14 * 60, EndMinute: 16 * 60},
	}
	loc := time.UTC
	for _, when := range []string{"2026-05-06T10:00:00Z", "2026-05-06T15:00:00Z"} {
		ts, _ := time.ParseInLocation(time.RFC3339, when, loc)
		if on, _ := evaluateWindows(schedules, ts); !on {
			t.Errorf("%s: expected true", when)
		}
	}
	ts, _ := time.ParseInLocation(time.RFC3339, "2026-05-06T13:00:00Z", loc)
	if on, _ := evaluateWindows(schedules, ts); on {
		t.Errorf("13:00 (gap): expected false")
	}
}

func TestEvaluateWindows_EmptyReturnsFalse(t *testing.T) {
	on, until := evaluateWindows(nil, time.Now())
	if on || !until.IsZero() {
		t.Errorf("empty schedules: got on=%v until=%v", on, until)
	}
}
