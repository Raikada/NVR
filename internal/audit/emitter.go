// Package audit holds the per-domain audit emitter helpers. The
// recorder's API/handler layer constructs an Emitter once at startup
// (Core wires it via store.AuditLogRepo) and threads sub-emitters
// (Auth, Camera, Policy, Clip, Event, Notification, System) through
// every handler that records mutations.
//
// Each sub-emitter helper accepts only the parameters explicitly
// allowed for that action, and never accepts secret material
// (passwords, ciphertext, tokens). Allow-list redaction at the call
// site is the design rule per ADR 0006 D1: handlers cannot
// accidentally leak secrets into the audit chain because there is no
// path for the secret to reach Insert.
package audit

import (
	"context"
	"time"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

// Emitter wraps the recorder's AuditLogRepo and exposes per-domain
// helper sub-emitters that each handler uses to record a single audit
// row.
type Emitter struct {
	repo *store.AuditLogRepo
}

// New constructs an Emitter pinned to the supplied repo.
func New(repo *store.AuditLogRepo) *Emitter {
	return &Emitter{repo: repo}
}

// Repo returns the underlying repository. Tests use this to assert
// rows were appended; production code should prefer the per-domain
// helpers.
func (e *Emitter) Repo() *store.AuditLogRepo {
	if e == nil {
		return nil
	}
	return e.repo
}

// Auth returns the auth-domain sub-emitter.
func (e *Emitter) Auth() *AuthEmitter { return &AuthEmitter{e} }

// Camera returns the camera-domain sub-emitter.
func (e *Emitter) Camera() *CameraEmitter { return &CameraEmitter{e} }

// Policy returns the recording-policy sub-emitter.
func (e *Emitter) Policy() *PolicyEmitter { return &PolicyEmitter{e} }

// Clip returns the clip-domain sub-emitter.
func (e *Emitter) Clip() *ClipEmitter { return &ClipEmitter{e} }

// Event returns the event-domain sub-emitter.
func (e *Emitter) Event() *EventEmitter { return &EventEmitter{e} }

// Notification returns the notification-domain sub-emitter.
func (e *Emitter) Notification() *NotificationEmitter { return &NotificationEmitter{e} }

// System returns the system-domain sub-emitter.
func (e *Emitter) System() *SystemEmitter { return &SystemEmitter{e} }

// User returns the user-management sub-emitter.
func (e *Emitter) User() *UserEmitter { return &UserEmitter{e} }

// PermissionDenied implements rbac.AuditEmitter so an *Emitter can be
// passed directly to rbac.RequirePerm. The middleware records a single
// auth.permission_denied row when a request fails the role gate.
func (e *Emitter) PermissionDenied(ctx context.Context, claims rbac.Claims, action, ip string) {
	if e == nil || e.repo == nil {
		return
	}
	_ = e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   claims.UserID,
		ActorUsername: claims.Username,
		ActorIP:       ip,
		Action:        "auth.permission_denied",
		TargetKind:    "session",
		Details:       "role=" + string(claims.Role) + " endpoint=" + action,
	})
}

// AuthEmitter records auth-domain audit rows. The recorder logs
// successful logins, failures, and explicit permission denials.
type AuthEmitter struct{ e *Emitter }

// LoginSuccess records a successful login. username + ip are surfaced
// in the chain; the password and hash are NEVER passed in.
func (a *AuthEmitter) LoginSuccess(ctx context.Context, userID, username, ip string) {
	if a == nil || a.e == nil || a.e.repo == nil {
		return
	}
	_ = a.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   userID,
		ActorUsername: username,
		ActorIP:       ip,
		Action:        "auth.login_success",
		TargetKind:    "session",
	})
}

// LoginFailure records a failed login. The chain captures only the
// username attempted + the source IP — not the password and not the
// reason (lockout, wrong password, inactive) to avoid hinting at
// account state.
func (a *AuthEmitter) LoginFailure(ctx context.Context, username, ip string) {
	if a == nil || a.e == nil || a.e.repo == nil {
		return
	}
	_ = a.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUsername: username,
		ActorIP:       ip,
		Action:        "auth.login_failure",
		TargetKind:    "session",
	})
}

// PermissionDenied records a permission-denied event. action is the
// HTTP path or canonical permission name; the sub-emitter intentionally
// surfaces no body or query parameters.
func (a *AuthEmitter) PermissionDenied(ctx context.Context, userID, username, action, ip string) {
	if a == nil || a.e == nil || a.e.repo == nil {
		return
	}
	_ = a.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   userID,
		ActorUsername: username,
		ActorIP:       ip,
		Action:        "auth.permission_denied",
		TargetKind:    "session",
		Details:       action,
	})
}

// PasswordChanged records a forced or operator-initiated password
// change. The chain captures only the user id; passwords never reach
// this helper.
func (a *AuthEmitter) PasswordChanged(ctx context.Context, userID, username, ip string) {
	if a == nil || a.e == nil || a.e.repo == nil {
		return
	}
	_ = a.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   userID,
		ActorUsername: username,
		ActorIP:       ip,
		Action:        "auth.password_changed",
		TargetKind:    "user",
		TargetID:      userID,
	})
}

// CameraEmitter records camera-domain mutations.
type CameraEmitter struct{ e *Emitter }

// Created records a camera-create. cameraName is captured so the
// audit row is human-readable; SourceURL / credentials are NOT
// captured.
func (c *CameraEmitter) Created(ctx context.Context, actorUserID, actorUsername, ip, cameraID, cameraName string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "camera.created",
		TargetKind:    "camera",
		TargetID:      cameraID,
		Details:       cameraName,
	})
}

// Updated records a camera update. fields is a comma-separated list of
// changed columns (e.g. "display_name,recording_policy_id"); never
// the new values.
func (c *CameraEmitter) Updated(ctx context.Context, actorUserID, actorUsername, ip, cameraID, fields string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "camera.updated",
		TargetKind:    "camera",
		TargetID:      cameraID,
		Details:       fields,
	})
}

// Deleted records a camera delete. The chain captures only the id.
func (c *CameraEmitter) Deleted(ctx context.Context, actorUserID, actorUsername, ip, cameraID string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "camera.deleted",
		TargetKind:    "camera",
		TargetID:      cameraID,
	})
}

// CredentialsRotated records a credential rotation. The chain captures
// only the camera id — username, ciphertext, and nonce never reach
// this helper. The point of the row is the operator-visible "this
// camera's credentials were changed at T", not the credential content.
func (c *CameraEmitter) CredentialsRotated(ctx context.Context, actorUserID, actorUsername, ip, cameraID string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "camera.credentials_rotated",
		TargetKind:    "camera",
		TargetID:      cameraID,
	})
}

// PolicyEmitter records recording-policy mutations.
type PolicyEmitter struct{ e *Emitter }

// Created records a policy create.
func (p *PolicyEmitter) Created(ctx context.Context, actorUserID, actorUsername, ip, policyID, name string) {
	if p == nil || p.e == nil || p.e.repo == nil {
		return
	}
	_ = p.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "policy.created",
		TargetKind:    "recording_policy",
		TargetID:      policyID,
		Details:       name,
	})
}

// Updated records a policy update. fields is a comma-separated changed-
// fields list.
func (p *PolicyEmitter) Updated(ctx context.Context, actorUserID, actorUsername, ip, policyID, fields string) {
	if p == nil || p.e == nil || p.e.repo == nil {
		return
	}
	_ = p.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "policy.updated",
		TargetKind:    "recording_policy",
		TargetID:      policyID,
		Details:       fields,
	})
}

// Deleted records a policy delete.
func (p *PolicyEmitter) Deleted(ctx context.Context, actorUserID, actorUsername, ip, policyID string) {
	if p == nil || p.e == nil || p.e.repo == nil {
		return
	}
	_ = p.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "policy.deleted",
		TargetKind:    "recording_policy",
		TargetID:      policyID,
	})
}

// ClipEmitter records clip-domain mutations.
type ClipEmitter struct{ e *Emitter }

// Created records a clip-prep request.
func (c *ClipEmitter) Created(ctx context.Context, actorUserID, actorUsername, ip, clipID string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "clip.created",
		TargetKind:    "clip",
		TargetID:      clipID,
	})
}

// Downloaded records a clip download.
func (c *ClipEmitter) Downloaded(ctx context.Context, actorUserID, actorUsername, ip, clipID string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "clip.downloaded",
		TargetKind:    "clip",
		TargetID:      clipID,
	})
}

// Deleted records a clip delete.
func (c *ClipEmitter) Deleted(ctx context.Context, actorUserID, actorUsername, ip, clipID string) {
	if c == nil || c.e == nil || c.e.repo == nil {
		return
	}
	_ = c.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "clip.deleted",
		TargetKind:    "clip",
		TargetID:      clipID,
	})
}

// EventEmitter records event-domain mutations (acknowledge, retention).
type EventEmitter struct{ e *Emitter }

// Acknowledged records an event ack.
func (ev *EventEmitter) Acknowledged(ctx context.Context, actorUserID, actorUsername, ip, eventID string) {
	if ev == nil || ev.e == nil || ev.e.repo == nil {
		return
	}
	_ = ev.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "event.acknowledged",
		TargetKind:    "event",
		TargetID:      eventID,
	})
}

// RetentionUpdated records an event-retention setting change.
func (ev *EventEmitter) RetentionUpdated(ctx context.Context, actorUserID, actorUsername, ip, eventTypeID string) {
	if ev == nil || ev.e == nil || ev.e.repo == nil {
		return
	}
	_ = ev.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "event_retention.updated",
		TargetKind:    "event_type",
		TargetID:      eventTypeID,
	})
}

// NotificationEmitter records notification-target / subscription
// mutations and outbox delivery outcomes.
type NotificationEmitter struct{ e *Emitter }

// TargetCreated records a notification-target create. The webhook URL
// is captured (not secret) but the signing secret is NEVER passed in.
func (n *NotificationEmitter) TargetCreated(ctx context.Context, actorUserID, actorUsername, ip, targetID, kind, name string) {
	if n == nil || n.e == nil || n.e.repo == nil {
		return
	}
	_ = n.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "notification_target.created",
		TargetKind:    "notification_target",
		TargetID:      targetID,
		Details:       kind + " " + name,
	})
}

// TargetDeleted records a notification-target delete.
func (n *NotificationEmitter) TargetDeleted(ctx context.Context, actorUserID, actorUsername, ip, targetID string) {
	if n == nil || n.e == nil || n.e.repo == nil {
		return
	}
	_ = n.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "notification_target.deleted",
		TargetKind:    "notification_target",
		TargetID:      targetID,
	})
}

// SubscriptionCreated records a subscription create.
func (n *NotificationEmitter) SubscriptionCreated(ctx context.Context, actorUserID, actorUsername, ip, subID string) {
	if n == nil || n.e == nil || n.e.repo == nil {
		return
	}
	_ = n.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "notification_subscription.created",
		TargetKind:    "notification_subscription",
		TargetID:      subID,
	})
}

// SubscriptionDeleted records a subscription delete.
func (n *NotificationEmitter) SubscriptionDeleted(ctx context.Context, actorUserID, actorUsername, ip, subID string) {
	if n == nil || n.e == nil || n.e.repo == nil {
		return
	}
	_ = n.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "notification_subscription.deleted",
		TargetKind:    "notification_subscription",
		TargetID:      subID,
	})
}

// SystemEmitter records system-level mutations.
type SystemEmitter struct{ e *Emitter }

// SettingChanged records a single system_settings update. Sensitive
// keys (smtp_password, *_ciphertext, *_nonce) MUST NOT be passed
// through this helper; the caller filters those at the API layer.
func (s *SystemEmitter) SettingChanged(ctx context.Context, actorUserID, actorUsername, ip, key string) {
	if s == nil || s.e == nil || s.e.repo == nil {
		return
	}
	_ = s.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "system.setting_changed",
		TargetKind:    "system",
		TargetID:      key,
	})
}

// TLSReplaced records a TLS cert+key replacement.
func (s *SystemEmitter) TLSReplaced(ctx context.Context, actorUserID, actorUsername, ip string) {
	if s == nil || s.e == nil || s.e.repo == nil {
		return
	}
	_ = s.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "system.tls_replaced",
		TargetKind:    "system",
	})
}

// TLSReloadFailed records a parse-failure on the TLS hot-reload path.
// reason is the parser error message; it carries no secret material.
func (s *SystemEmitter) TLSReloadFailed(ctx context.Context, reason string) {
	if s == nil || s.e == nil || s.e.repo == nil {
		return
	}
	_ = s.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt: time.Now().UTC(),
		Action:     "system.tls_reload_failed",
		TargetKind: "system",
		Details:    reason,
	})
}

// RetentionSwept records an operator-triggered retention sweep.
func (s *SystemEmitter) RetentionSwept(ctx context.Context, actorUserID, actorUsername, ip string) {
	if s == nil || s.e == nil || s.e.repo == nil {
		return
	}
	_ = s.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "system.retention_swept",
		TargetKind:    "system",
	})
}

// UserEmitter records user-management mutations.
type UserEmitter struct{ e *Emitter }

// Created records a user create. The role + active flag are surfaced;
// the password hash is NEVER passed in.
func (u *UserEmitter) Created(ctx context.Context, actorUserID, actorUsername, ip, userID, username, role string) {
	if u == nil || u.e == nil || u.e.repo == nil {
		return
	}
	_ = u.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "user.created",
		TargetKind:    "user",
		TargetID:      userID,
		Details:       username + " role=" + role,
	})
}

// Updated records a user update.
func (u *UserEmitter) Updated(ctx context.Context, actorUserID, actorUsername, ip, userID, fields string) {
	if u == nil || u.e == nil || u.e.repo == nil {
		return
	}
	_ = u.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "user.updated",
		TargetKind:    "user",
		TargetID:      userID,
		Details:       fields,
	})
}

// Deleted records a user delete.
func (u *UserEmitter) Deleted(ctx context.Context, actorUserID, actorUsername, ip, userID string) {
	if u == nil || u.e == nil || u.e.repo == nil {
		return
	}
	_ = u.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "user.deleted",
		TargetKind:    "user",
		TargetID:      userID,
	})
}

// PasswordReset records an admin-initiated password reset on another
// user's account. The reset never carries the new password.
func (u *UserEmitter) PasswordReset(ctx context.Context, actorUserID, actorUsername, ip, targetUserID string) {
	if u == nil || u.e == nil || u.e.repo == nil {
		return
	}
	_ = u.e.repo.Insert(ctx, &store.AuditEntry{
		OccurredAt:    time.Now().UTC(),
		ActorUserID:   actorUserID,
		ActorUsername: actorUsername,
		ActorIP:       ip,
		Action:        "user.password_reset",
		TargetKind:    "user",
		TargetID:      targetUserID,
	})
}
