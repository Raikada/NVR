package notifications

import (
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
)

func TestMatches_NoFilters(t *testing.T) {
	sub := SubscriptionView{TargetID: "t1", QuietHoursStartMinute: -1, QuietHoursEndMinute: -1}
	ev := events.Event{TypeID: "motion", CameraID: "cam-1"}
	if !matches(sub, ev, time.Now()) {
		t.Errorf("expected match for unfiltered subscription")
	}
}

func TestMatches_TypeFilter(t *testing.T) {
	sub := SubscriptionView{
		TargetID: "t1", EventTypeID: "motion",
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	if matches(sub, events.Event{TypeID: "tamper"}, time.Now()) {
		t.Errorf("type mismatch should not match")
	}
	if !matches(sub, events.Event{TypeID: "motion"}, time.Now()) {
		t.Errorf("type match should match")
	}
}

func TestMatches_CameraFilter(t *testing.T) {
	sub := SubscriptionView{
		TargetID: "t1", CameraID: "cam-1",
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	if matches(sub, events.Event{CameraID: "cam-2"}, time.Now()) {
		t.Errorf("camera mismatch should not match")
	}
	if !matches(sub, events.Event{CameraID: "cam-1"}, time.Now()) {
		t.Errorf("camera match should match")
	}
}

func TestMatches_MinSeverity(t *testing.T) {
	sub := SubscriptionView{
		TargetID: "t1", MinSeverity: "warning",
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	cases := []struct {
		ev   string
		want bool
	}{
		{"info", false},
		{"warning", true},
		{"critical", true},
	}
	for _, tc := range cases {
		if got := matches(sub, events.Event{Severity: tc.ev}, time.Now()); got != tc.want {
			t.Errorf("severity %q: got %v want %v", tc.ev, got, tc.want)
		}
	}
}

func TestMatches_QuietHoursForward(t *testing.T) {
	// Quiet from 22:00 to 06:00 (wraps midnight)
	sub := SubscriptionView{
		TargetID: "t1",
		QuietHoursStartMinute: 22 * 60,
		QuietHoursEndMinute:   6 * 60,
	}
	ev := events.Event{}
	cases := []struct {
		hour int
		min  int
		want bool // true == match (NOT in quiet hours)
	}{
		{12, 0, true},  // noon: not quiet
		{23, 0, false}, // 23:00: quiet
		{2, 0, false},  // 02:00: quiet
		{6, 0, true},   // 06:00: out (exclusive end == start of allowed)
	}
	for _, tc := range cases {
		now := time.Date(2026, 5, 7, tc.hour, tc.min, 0, 0, time.UTC)
		if got := matches(sub, ev, now); got != tc.want {
			t.Errorf("%02d:%02d: got %v want %v", tc.hour, tc.min, got, tc.want)
		}
	}
}

func TestInQuietHours_NonWrapping(t *testing.T) {
	// 13:00..17:00
	if !inQuietHours(14*60, 13*60, 17*60) {
		t.Errorf("14:00 should be in quiet 13-17")
	}
	if inQuietHours(12*60, 13*60, 17*60) {
		t.Errorf("12:00 should NOT be in quiet 13-17")
	}
}

func TestHexToBytes(t *testing.T) {
	got, err := hexToBytes("")
	if err != nil || got != nil {
		t.Errorf("empty: got %v err %v", got, err)
	}
	got, err = hexToBytes("deadbeef")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 4 || got[0] != 0xde {
		t.Errorf("decoded %v", got)
	}
	if _, err := hexToBytes("not-hex"); err == nil {
		t.Errorf("expected decode error on bad hex")
	}
}
