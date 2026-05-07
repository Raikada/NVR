package bootstrap

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

// seedSettings populates runtime-only system_settings rows: timezone
// (from time.Local when unset), snapshot_root, and clip_root (derived
// from opts.RecordingsRoot when supplied).
//
// Migration 0023 pre-seeds many settings as empty strings; the helper
// treats "missing row" and "empty string" as equivalent so a fresh
// recorder gets sensible runtime values for every key the foundation
// needs without overwriting operator-customized values.
func seedSettings(ctx context.Context, st *store.Store, opts Options, res *Result) error {
	defaults := map[string]string{}

	if needsValue(ctx, st, "timezone") {
		defaults["timezone"] = time.Local.String()
	}

	if opts.RecordingsRoot != "" {
		if needsValue(ctx, st, "snapshot_root") {
			defaults["snapshot_root"] = filepath.Join(opts.RecordingsRoot, "snapshots")
		}
		if needsValue(ctx, st, "clip_root") {
			defaults["clip_root"] = filepath.Join(opts.RecordingsRoot, "clips")
		}
	}

	if len(defaults) == 0 {
		return nil
	}
	if err := st.SystemSettings.UpsertMany(ctx, defaults, ""); err != nil {
		return fmt.Errorf("bootstrap: upsert settings: %w", err)
	}
	res.SettingsSeeded = len(defaults)
	return nil
}

// needsValue returns true if the setting at key is missing OR present
// with an empty value (migration 0023 seeds many keys as "").
func needsValue(ctx context.Context, st *store.Store, key string) bool {
	s, err := st.SystemSettings.Get(ctx, key)
	if err != nil {
		return true
	}
	return s == nil || s.Value == ""
}
