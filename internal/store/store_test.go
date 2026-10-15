package store

import (
	"testing"
)

// F8 (2026-07-02 smoke): ParseTime must accept any RFC3339 timestamp,
// not only the exact .000 fractional form FormatTime emits — rows
// written by migrations (strftime) or external tools use plain RFC3339.
func TestParseTimeAcceptsRFC3339Variants(t *testing.T) {
	cases := []string{
		"2026-07-02T16:15:36.000Z",  // FormatTime's own output
		"2026-07-02T16:15:36Z",      // plain RFC3339
		"2026-07-02T16:15:36.123456789Z", // nanoseconds
		"2026-07-02T11:15:36-05:00", // zoned
	}
	for _, s := range cases {
		if _, err := ParseTime(s); err != nil {
			t.Fatalf("ParseTime(%q): %v", s, err)
		}
	}
	if _, err := ParseTime("not a time"); err == nil {
		t.Fatal("garbage must still error")
	}
}
