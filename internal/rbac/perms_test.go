package rbac

import "testing"

func TestHasPermission(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		// Admin: everything is granted.
		{RoleAdmin, PermCameraCreate, true},
		{RoleAdmin, PermCameraDelete, true},
		{RoleAdmin, PermCameraCredentialsWrite, true},
		{RoleAdmin, PermPolicyDelete, true},
		{RoleAdmin, PermPolicyScheduleWrite, true},
		{RoleAdmin, PermNotificationTargetTest, true},
		{RoleAdmin, PermAuditPurge, true},
		{RoleAdmin, PermSystemTLSWrite, true},

		// Viewer: read-only / playback / live-view granted.
		{RoleViewer, PermCameraList, true},
		{RoleViewer, PermCameraRead, true},
		{RoleViewer, PermCameraLiveView, true},
		{RoleViewer, PermEventList, true},
		{RoleViewer, PermEventRead, true},
		{RoleViewer, PermEventStream, true},
		{RoleViewer, PermClipDownload, true},
		{RoleViewer, PermRecordingPlayback, true},
		{RoleViewer, PermSystemSettingsRead, true},
		{RoleViewer, PermAuditRead, true},

		// Viewer: write/admin-only paths denied.
		{RoleViewer, PermCameraCreate, false},
		{RoleViewer, PermCameraUpdate, false},
		{RoleViewer, PermCameraDelete, false},
		{RoleViewer, PermCameraCredentialsWrite, false},
		{RoleViewer, PermPolicyCreate, false},
		{RoleViewer, PermPolicyScheduleWrite, false},
		{RoleViewer, PermUserCreate, false},
		{RoleViewer, PermUserDelete, false},
		{RoleViewer, PermNotificationTargetCreate, false},
		{RoleViewer, PermNotificationTargetTest, false},
		{RoleViewer, PermSystemSettingsWrite, false},
		{RoleViewer, PermSystemTLSWrite, false},
		{RoleViewer, PermAuditPurge, false},

		// Unknown role: fail closed.
		{Role("nonexistent"), PermCameraRead, false},
		{Role("nonexistent"), PermCameraList, false},
		{Role(""), PermCameraRead, false},
	}
	for _, tc := range cases {
		got := HasPermission(tc.role, tc.perm)
		if got != tc.want {
			t.Errorf("HasPermission(%q, %q) = %v, want %v", tc.role, tc.perm, got, tc.want)
		}
	}
}

func TestAllPermissionsFor(t *testing.T) {
	admin := AllPermissionsFor(RoleAdmin)
	if len(admin) != len(allPermissions) {
		t.Errorf("admin should grant every defined permission (%d), got %d",
			len(allPermissions), len(admin))
	}

	viewer := AllPermissionsFor(RoleViewer)
	if len(viewer) != len(viewerPermissions) {
		t.Errorf("viewer should grant len(viewerPermissions)=%d, got %d",
			len(viewerPermissions), len(viewer))
	}
	if len(viewer) >= len(admin) {
		t.Errorf("viewer set should be strictly smaller than admin: viewer=%d admin=%d",
			len(viewer), len(admin))
	}

	if got := AllPermissionsFor(Role("nope")); got != nil {
		t.Errorf("unknown role should yield nil, got %v", got)
	}
}

func TestRoleGrants_NoDuplicateConstants(t *testing.T) {
	// Sanity check: every constant in allPermissions is unique. Dupes
	// would silently shadow each other in roleGrants[RoleAdmin].
	seen := make(map[Permission]struct{}, len(allPermissions))
	for _, p := range allPermissions {
		if _, ok := seen[p]; ok {
			t.Errorf("duplicate permission constant: %q", p)
		}
		seen[p] = struct{}{}
	}
}
