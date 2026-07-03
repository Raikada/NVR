package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrations_AllTablesExist verifies that opening the DB applies every
// migration through 0023 and creates each expected table. We probe each
// table with `SELECT COUNT(*)` (returns 0 on empty); if a table is missing
// SQLite returns "no such table" which fails the test.
func TestMigrations_AllTablesExist(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "recorder.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	tables := []string{
		// 0001-0002 (existing)
		"local_users",
		"onvif_subscriptions",
		// 0003-0023 (new)
		"roles",
		"camera_groups",
		"cameras",
		"camera_credentials",
		"camera_capabilities",
		"camera_health",
		"recording_policies",
		"recording_schedules",
		"event_types",
		"event_retention",
		"events",
		"event_snapshots",
		"clips",
		"clip_segments",
		"notification_targets",
		"notification_subscriptions",
		"notification_outbox",
		"audit_log",
		"cloud_outbox",
		"system_settings",
	}
	ctx := context.Background()
	for _, table := range tables {
		var n int
		row := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table)
		if err := row.Scan(&n); err != nil {
			t.Errorf("table %s: %v", table, err)
		}
	}
}

// TestMigrations_SeedRows verifies that the migrations that include INSERT
// statements left their rows behind.
func TestMigrations_SeedRows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "recorder.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	cases := []struct {
		query    string
		expected int
		desc     string
	}{
		{"SELECT COUNT(*) FROM roles", 2, "roles seeded with admin + viewer"},
		{"SELECT COUNT(*) FROM recording_policies WHERE id='policy_default'", 1, "default policy seeded"},
		{"SELECT COUNT(*) FROM event_types", 12, "event types seeded"},
		{"SELECT COUNT(*) FROM event_retention WHERE type_id='__default__'", 1, "default retention seeded"},
		{"SELECT COUNT(*) FROM system_settings WHERE key='lockout_threshold'", 1, "system_settings seeded"},
	}
	for _, tc := range cases {
		var n int
		if err := s.DB.QueryRow(tc.query).Scan(&n); err != nil {
			t.Errorf("%s: %v", tc.desc, err)
			continue
		}
		if n != tc.expected {
			t.Errorf("%s: expected %d rows, got %d", tc.desc, tc.expected, n)
		}
	}
}
