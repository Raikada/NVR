package rbac

import "testing"

// Phase A B2: owner/operator/audit roles join admin/viewer.
func TestPhaseARoles(t *testing.T) {
	cases := []struct {
		role Role
		yes  []Permission
		no   []Permission
	}{
		{RoleOwner,
			[]Permission{PermUserCreate, PermSystemSettingsWrite, PermCameraPTZ, PermCameraDelete, PermAuditPurge},
			nil},
		{RoleOperator,
			[]Permission{PermCameraList, PermEventAck, PermClipCreate, PermClipDownload, PermCameraPTZ, PermEventStream, PermRecordingPlayback},
			[]Permission{PermUserCreate, PermCameraDelete, PermSystemSettingsWrite, PermPolicyUpdate}},
		{RoleAudit,
			[]Permission{PermAuditRead, PermEventList, PermUserList, PermSystemSettingsRead},
			[]Permission{PermAuditPurge, PermEventAck, PermCameraLiveView, PermClipCreate, PermUserCreate}},
	}
	for _, tc := range cases {
		for _, p := range tc.yes {
			if !HasPermission(tc.role, p) {
				t.Fatalf("%s must grant %s", tc.role, p)
			}
		}
		for _, p := range tc.no {
			if HasPermission(tc.role, p) {
				t.Fatalf("%s must NOT grant %s", tc.role, p)
			}
		}
	}
	// Unknown roles still fail closed.
	if HasPermission(Role("pigeon"), PermCameraList) {
		t.Fatal("unknown role must fail closed")
	}
	// Owner mirrors admin's full set.
	if len(AllPermissionsFor(RoleOwner)) != len(AllPermissionsFor(RoleAdmin)) {
		t.Fatal("owner must carry the full admin permission set")
	}
}
