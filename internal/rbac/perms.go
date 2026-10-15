// Package rbac defines the consumer NVR's role and permission enums and
// the role->permission map. The map is in code (not in DB) per the
// foundation spec; promoting to DB-defined custom roles is a future
// migration. The package also exposes a Gin middleware factory that
// reads role from JWT claims and returns 403 on missing permission.
package rbac

// Role is a string enum matching the seeded roles.id values in DB.
type Role string

const (
	// RoleAdmin grants every permission.
	RoleAdmin Role = "admin"
	// RoleViewer grants the read-only / live-view subset.
	RoleViewer Role = "viewer"
	// RoleOwner is the account-holder tier: everything admin has.
	// Distinct so a future cloud/ownership model can diverge them
	// without a migration.
	RoleOwner Role = "owner"
	// RoleOperator is viewer + day-to-day actions: acknowledge events,
	// export clips, drive PTZ. No configuration mutation.
	RoleOperator Role = "operator"
	// RoleAudit is the compliance tier: read logs/metadata, no media
	// surfaces, no mutations.
	RoleAudit Role = "audit"
)

// Permission is a string enum used by the API middleware to gate
// handlers. Permissions are dot-namespaced for grouping (camera.*,
// policy.*, event.*, ...). The granular scheme matches the existing
// internal/api/api.go's a.requirePermission(...) call sites and adds
// new constants for the foundation surfaces.
type Permission string

const (
	// camera.* — base CRUD already used by api.go.
	PermCameraList   Permission = "camera.list"
	PermCameraRead   Permission = "camera.read"
	PermCameraCreate Permission = "camera.create"
	PermCameraUpdate Permission = "camera.update"
	PermCameraDelete Permission = "camera.delete"
	// camera.* — foundation extensions.
	PermCameraProbe            Permission = "camera.probe"
	PermCameraCredentialsWrite Permission = "camera.credentials.write"
	// camera.live.view is preserved from existing api.go usage.
	PermCameraLiveView Permission = "camera.live.view"

	// Phase A B5 surfaces, declared with the role work (B2) so the
	// role sets reference real constants.
	PermCameraPTZ    Permission = "camera.ptz"
	PermCameraReboot Permission = "camera.reboot"

	// camera_group.* — new in foundation.
	PermCameraGroupList   Permission = "camera_group.list"
	PermCameraGroupRead   Permission = "camera_group.read"
	PermCameraGroupCreate Permission = "camera_group.create"
	PermCameraGroupUpdate Permission = "camera_group.update"
	PermCameraGroupDelete Permission = "camera_group.delete"

	// policy.* — base CRUD already used by api.go.
	PermPolicyList   Permission = "policy.list"
	PermPolicyRead   Permission = "policy.read"
	PermPolicyCreate Permission = "policy.create"
	PermPolicyUpdate Permission = "policy.update"
	PermPolicyDelete Permission = "policy.delete"
	// policy.schedule.* — new in foundation.
	PermPolicyScheduleRead  Permission = "policy.schedule.read"
	PermPolicyScheduleWrite Permission = "policy.schedule.write"

	// recording.* — base, used by api.go.
	PermRecordingList     Permission = "recording.list"
	PermRecordingRead     Permission = "recording.read"
	PermRecordingPlayback Permission = "recording.playback"
	PermRecordingDelete   Permission = "recording.delete"

	// recording_segment.* — used by api.go.
	PermRecordingSegmentList   Permission = "recording_segment.list"
	PermRecordingSegmentRead   Permission = "recording_segment.read"
	PermRecordingSegmentDelete Permission = "recording_segment.delete"

	// stream.* — used by api.go.
	PermStreamList Permission = "stream.list"
	PermStreamRead Permission = "stream.read"
	PermStreamKick Permission = "stream.kick"

	// event.* — base.
	PermEventList   Permission = "event.list"
	PermEventRead   Permission = "event.read"
	PermEventAck    Permission = "event.ack"
	PermEventStream Permission = "event.stream"

	// event_type.* — new in foundation.
	PermEventTypeList   Permission = "event_type.list"
	PermEventTypeCreate Permission = "event_type.create"
	PermEventTypeUpdate Permission = "event_type.update"

	// event_retention.* — new in foundation.
	PermEventRetentionRead  Permission = "event_retention.read"
	PermEventRetentionWrite Permission = "event_retention.write"

	// notification_target.* — new in foundation.
	PermNotificationTargetList   Permission = "notification_target.list"
	PermNotificationTargetCreate Permission = "notification_target.create"
	PermNotificationTargetUpdate Permission = "notification_target.update"
	PermNotificationTargetDelete Permission = "notification_target.delete"
	PermNotificationTargetTest   Permission = "notification_target.test"

	// notification_subscription.* — new in foundation.
	PermNotificationSubscriptionList   Permission = "notification_subscription.list"
	PermNotificationSubscriptionCreate Permission = "notification_subscription.create"
	PermNotificationSubscriptionDelete Permission = "notification_subscription.delete"

	// notification_outbox.* — new in foundation.
	PermNotificationOutboxList  Permission = "notification_outbox.list"
	PermNotificationOutboxRetry Permission = "notification_outbox.retry"

	// clip.* — base, used by api.go.
	PermClipCreate   Permission = "clip.create"
	PermClipList     Permission = "clip.list"
	PermClipRead     Permission = "clip.read"
	PermClipDelete   Permission = "clip.delete"
	PermClipDownload Permission = "clip.download"

	// storage.* — used by api.go.
	PermStorageList Permission = "storage.list"
	PermStorageRead Permission = "storage.read"

	// user.* — new in foundation.
	PermUserList   Permission = "user.list"
	PermUserRead   Permission = "user.read"
	PermUserCreate Permission = "user.create"
	PermUserUpdate Permission = "user.update"
	PermUserDelete Permission = "user.delete"

	// system.* — new in foundation.
	PermSystemSettingsRead  Permission = "system.settings.read"
	PermSystemSettingsWrite Permission = "system.settings.write"
	PermSystemTLSWrite      Permission = "system.tls.write"
	PermSystemRetentionRun  Permission = "system.retention.run"

	// audit.* — base + new.
	PermAuditRead  Permission = "audit.read"
	PermAuditPurge Permission = "audit.purge"
)

// allPermissions is the master list of every Permission constant,
// returned unmodified to the admin role and used by tests to confirm
// completeness.
var allPermissions = []Permission{
	PermCameraList, PermCameraRead, PermCameraCreate, PermCameraUpdate, PermCameraDelete,
	PermCameraProbe, PermCameraCredentialsWrite, PermCameraLiveView,
	PermCameraGroupList, PermCameraGroupRead, PermCameraGroupCreate, PermCameraGroupUpdate, PermCameraGroupDelete,
	PermPolicyList, PermPolicyRead, PermPolicyCreate, PermPolicyUpdate, PermPolicyDelete,
	PermPolicyScheduleRead, PermPolicyScheduleWrite,
	PermRecordingList, PermRecordingRead, PermRecordingPlayback, PermRecordingDelete,
	PermRecordingSegmentList, PermRecordingSegmentRead, PermRecordingSegmentDelete,
	PermStreamList, PermStreamRead, PermStreamKick,
	PermEventList, PermEventRead, PermEventAck, PermEventStream,
	PermEventTypeList, PermEventTypeCreate, PermEventTypeUpdate,
	PermEventRetentionRead, PermEventRetentionWrite,
	PermNotificationTargetList, PermNotificationTargetCreate, PermNotificationTargetUpdate,
	PermNotificationTargetDelete, PermNotificationTargetTest,
	PermNotificationSubscriptionList, PermNotificationSubscriptionCreate, PermNotificationSubscriptionDelete,
	PermNotificationOutboxList, PermNotificationOutboxRetry,
	PermCameraPTZ, PermCameraReboot,
	PermClipCreate, PermClipList, PermClipRead, PermClipDelete, PermClipDownload,
	PermStorageList, PermStorageRead,
	PermUserList, PermUserRead, PermUserCreate, PermUserUpdate, PermUserDelete,
	PermSystemSettingsRead, PermSystemSettingsWrite, PermSystemTLSWrite, PermSystemRetentionRun,
	PermAuditRead, PermAuditPurge,
}

// viewerPermissions is the read-only / live-view subset granted to viewers.
// It includes every *.list and *.read permission plus the dynamic surfaces
// a viewer should still be able to consume (event.stream, clip.download,
// recording.playback, system.settings.read, camera.live.view).
var viewerPermissions = []Permission{
	PermCameraList, PermCameraRead, PermCameraLiveView,
	PermCameraGroupList, PermCameraGroupRead,
	PermPolicyList, PermPolicyRead, PermPolicyScheduleRead,
	PermRecordingList, PermRecordingRead, PermRecordingPlayback,
	PermRecordingSegmentList, PermRecordingSegmentRead,
	PermStreamList, PermStreamRead,
	PermEventList, PermEventRead, PermEventStream,
	PermEventTypeList, PermEventRetentionRead,
	PermNotificationTargetList, PermNotificationSubscriptionList, PermNotificationOutboxList,
	PermClipList, PermClipRead, PermClipDownload,
	PermStorageList, PermStorageRead,
	PermUserList, PermUserRead,
	PermSystemSettingsRead,
	PermAuditRead,
}

// operatorExtras are the day-to-day actions an operator has beyond a
// viewer: acknowledge, export, PTZ. No configuration mutation.
var operatorExtras = []Permission{
	PermEventAck, PermClipCreate, PermClipDownload, PermCameraPTZ,
}

// auditPermissions is the compliance subset: logs and metadata reads,
// no media surfaces (live view, playback, downloads), no mutations.
var auditPermissions = []Permission{
	PermAuditRead,
	PermCameraList, PermCameraRead,
	PermCameraGroupList, PermCameraGroupRead,
	PermPolicyList, PermPolicyRead, PermPolicyScheduleRead,
	PermEventList, PermEventRead, PermEventTypeList, PermEventRetentionRead,
	PermUserList, PermUserRead,
	PermSystemSettingsRead,
	PermStorageList, PermStorageRead,
}

// roleGrants is the in-code role->permission map (fail-closed for
// unknown roles). owner mirrors admin; operator = viewer + extras;
// audit is the compliance subset.
var roleGrants = func() map[Role]map[Permission]struct{} {
	m := map[Role]map[Permission]struct{}{
		RoleAdmin:    make(map[Permission]struct{}, len(allPermissions)),
		RoleOwner:    make(map[Permission]struct{}, len(allPermissions)),
		RoleViewer:   make(map[Permission]struct{}, len(viewerPermissions)),
		RoleOperator: make(map[Permission]struct{}, len(viewerPermissions)+len(operatorExtras)),
		RoleAudit:    make(map[Permission]struct{}, len(auditPermissions)),
	}
	for _, p := range allPermissions {
		m[RoleAdmin][p] = struct{}{}
		m[RoleOwner][p] = struct{}{}
	}
	for _, p := range viewerPermissions {
		m[RoleViewer][p] = struct{}{}
		m[RoleOperator][p] = struct{}{}
	}
	for _, p := range operatorExtras {
		m[RoleOperator][p] = struct{}{}
	}
	for _, p := range auditPermissions {
		m[RoleAudit][p] = struct{}{}
	}
	return m
}()

// HasPermission reports whether the given role grants the permission.
// Unknown roles never grant anything (fail closed).
func HasPermission(role Role, p Permission) bool {
	grants, ok := roleGrants[role]
	if !ok {
		return false
	}
	_, ok = grants[p]
	return ok
}

// AllPermissionsFor returns the permission set for the given role.
// Used by the API to ship the user's permissions to the SPA so the SPA
// can hide/show controls without a network round-trip.
func AllPermissionsFor(role Role) []Permission {
	grants, ok := roleGrants[role]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(grants))
	for p := range grants {
		out = append(out, p)
	}
	return out
}

// RoleFromID maps a roles-table id (role_admin, role_operator, …) to
// the rbac Role. Unknown ids return RoleViewer (fail toward least
// privilege).
func RoleFromID(roleID string) Role {
	switch roleID {
	case "role_admin":
		return RoleAdmin
	case "role_owner":
		return RoleOwner
	case "role_operator":
		return RoleOperator
	case "role_audit":
		return RoleAudit
	case "role_viewer":
		return RoleViewer
	}
	return RoleViewer
}

// RoleForScope infers the role from a token's scope set. Issued scopes
// are exactly AllPermissionsFor(role), so exact set-matching is
// deterministic; owner and admin share a set and resolve to admin at
// the claims layer (identical enforcement — display-layer role comes
// from the user row). Unrecognized shapes fall to viewer.
func RoleForScope(scope []string) Role {
	set := make(map[Permission]struct{}, len(scope))
	for _, s := range scope {
		set[Permission(s)] = struct{}{}
	}
	matches := func(role Role) bool {
		grants := roleGrants[role]
		if len(grants) != len(set) {
			return false
		}
		for p := range grants {
			if _, ok := set[p]; !ok {
				return false
			}
		}
		return true
	}
	for _, r := range []Role{RoleAdmin, RoleOperator, RoleAudit, RoleViewer} {
		if matches(r) {
			return r
		}
	}
	// Superset of admin (legacy tokens with extra strings) → admin.
	admin := roleGrants[RoleAdmin]
	all := true
	for p := range admin {
		if _, ok := set[p]; !ok {
			all = false
			break
		}
	}
	if all {
		return RoleAdmin
	}
	return RoleViewer
}
