// Package api: /v1/notification-{targets,subscriptions,outbox}
// handlers per Phase 5 Task 5.6.
//
// Targets carry a webhook URL + per-target HMAC secret (encrypted via
// the vault on persist) OR an email address. Secrets are accepted in
// plaintext on the request body and immediately encrypted; the response
// surfaces only the URL/email + an enabled flag — never the secret.
//
// After every mutation we call notifications.Dispatcher.InvalidateCache()
// so the next event fan-out re-loads the join table.
package api //nolint:revive

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1Notifications(r gin.IRouter) {
	r.GET("/notification-targets", rbac.RequirePerm(rbac.PermNotificationTargetList, a.auditEmitter()), a.onV1NotificationTargetsList)
	r.POST("/notification-targets", rbac.RequirePerm(rbac.PermNotificationTargetCreate, a.auditEmitter()), a.onV1NotificationTargetsCreate)
	r.PATCH("/notification-targets/:id", rbac.RequirePerm(rbac.PermNotificationTargetUpdate, a.auditEmitter()), a.onV1NotificationTargetsUpdate)
	r.DELETE("/notification-targets/:id", rbac.RequirePerm(rbac.PermNotificationTargetDelete, a.auditEmitter()), a.onV1NotificationTargetsDelete)
	r.POST("/notification-targets/:id/test", rbac.RequirePerm(rbac.PermNotificationTargetTest, a.auditEmitter()), a.onV1NotificationTargetTest)

	r.GET("/notification-subscriptions", rbac.RequirePerm(rbac.PermNotificationSubscriptionList, a.auditEmitter()), a.onV1NotificationSubscriptionsList)
	r.POST("/notification-subscriptions", rbac.RequirePerm(rbac.PermNotificationSubscriptionCreate, a.auditEmitter()), a.onV1NotificationSubscriptionsCreate)
	r.DELETE("/notification-subscriptions/:id", rbac.RequirePerm(rbac.PermNotificationSubscriptionDelete, a.auditEmitter()), a.onV1NotificationSubscriptionsDelete)

	r.GET("/notification-outbox", rbac.RequirePerm(rbac.PermNotificationOutboxList, a.auditEmitter()), a.onV1NotificationOutboxList)
	r.POST("/notification-outbox/:id/retry", rbac.RequirePerm(rbac.PermNotificationOutboxRetry, a.auditEmitter()), a.onV1NotificationOutboxRetry)
}

// ----- targets ------------------------------------------------------

type notificationTargetWire struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"` // never set on responses
	WebhookSecretSet bool `json:"webhook_secret_set,omitempty"`
	EmailAddress  string `json:"email_address,omitempty"`
	Enabled       bool   `json:"enabled"`
	CreatedAt     string `json:"created_at"`
}

func notificationTargetToWire(t *store.NotificationTarget) notificationTargetWire {
	return notificationTargetWire{
		ID: t.ID, Kind: t.Kind, Name: t.Name,
		WebhookURL: t.WebhookURL,
		WebhookSecretSet: len(t.WebhookSecretCiphertext) > 0,
		EmailAddress: t.EmailAddress,
		Enabled: t.Enabled,
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (a *API) onV1NotificationTargetsList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	rows, err := a.Store.NotificationTargets.List(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]notificationTargetWire, 0, len(rows))
	for _, t := range rows {
		out = append(out, notificationTargetToWire(t))
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

type notificationTargetCreateRequest struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	WebhookURL    string `json:"webhook_url"`
	WebhookSecret string `json:"webhook_secret"`
	EmailAddress  string `json:"email_address"`
	Enabled       *bool  `json:"enabled"`
}

func (a *API) onV1NotificationTargetsCreate(ctx *gin.Context) {
	if a.Store == nil || a.Vault == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("notifications stack not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req notificationTargetCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Kind == "" || req.Name == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("kind and name are required"))
		return
	}
	t := &store.NotificationTarget{
		ID: uuid.NewString(), Kind: req.Kind, Name: req.Name,
		WebhookURL: req.WebhookURL, EmailAddress: req.EmailAddress,
		Enabled: true,
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Kind == "webhook" {
		if req.WebhookURL == "" {
			a.writeError(ctx, http.StatusBadRequest, errors.New("webhook_url is required for webhook targets"))
			return
		}
		if req.WebhookSecret != "" {
			ct, nonce, err := a.Vault.Encrypt([]byte(req.WebhookSecret))
			if err != nil {
				a.writeError(ctx, http.StatusInternalServerError, err)
				return
			}
			t.WebhookSecretCiphertext = ct
			t.WebhookSecretNonce = nonce
		}
	} else if req.Kind == "email" {
		if req.EmailAddress == "" {
			a.writeError(ctx, http.StatusBadRequest, errors.New("email_address is required for email targets"))
			return
		}
	} else {
		a.writeError(ctx, http.StatusBadRequest, errors.New("kind must be 'webhook' or 'email'"))
		return
	}
	if err := a.Store.NotificationTargets.Insert(ctx.Request.Context(), t); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.invalidateNotificationCache()
	a.emitMutationAudit(ctx, "notification_target.created", "notification_target", t.ID, map[string]string{
		"kind": t.Kind, "name": t.Name,
	})
	w := notificationTargetToWire(t)
	ctx.JSON(http.StatusCreated, &w)
}

type notificationTargetUpdateRequest struct {
	Name          *string `json:"name"`
	WebhookURL    *string `json:"webhook_url"`
	WebhookSecret *string `json:"webhook_secret"`
	EmailAddress  *string `json:"email_address"`
	Enabled       *bool   `json:"enabled"`
}

func (a *API) onV1NotificationTargetsUpdate(ctx *gin.Context) {
	if a.Store == nil || a.Vault == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("notifications stack not wired"))
		return
	}
	id := ctx.Param("id")
	existing, err := a.Store.NotificationTargets.GetByID(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("notification target not found"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req notificationTargetUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.WebhookURL != nil {
		existing.WebhookURL = *req.WebhookURL
	}
	if req.EmailAddress != nil {
		existing.EmailAddress = *req.EmailAddress
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if err := a.Store.NotificationTargets.Update(ctx.Request.Context(), existing); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if req.WebhookSecret != nil && *req.WebhookSecret != "" {
		ct, nonce, err := a.Vault.Encrypt([]byte(*req.WebhookSecret))
		if err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		if err := a.Store.NotificationTargets.RotateWebhookSecret(ctx.Request.Context(), id, ct, nonce); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		existing.WebhookSecretCiphertext = ct
		existing.WebhookSecretNonce = nonce
	}
	a.invalidateNotificationCache()
	a.emitMutationAudit(ctx, "notification_target.updated", "notification_target", id, nil)
	w := notificationTargetToWire(existing)
	ctx.JSON(http.StatusOK, &w)
}

func (a *API) onV1NotificationTargetsDelete(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	if err := a.Store.NotificationTargets.Delete(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotificationTargetNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.invalidateNotificationCache()
	a.emitMutationAudit(ctx, "notification_target.deleted", "notification_target", id, nil)
	ctx.Status(http.StatusNoContent)
}

func (a *API) onV1NotificationTargetTest(ctx *gin.Context) {
	if a.NotifDispatcher == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("notification dispatcher not wired"))
		return
	}
	id := ctx.Param("id")
	res, err := a.NotifDispatcher.SendTest(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "notification_target.tested", "notification_target", id, map[string]string{
		"kind": res.Kind,
	})
	ctx.JSON(http.StatusOK, &res)
}

// ----- subscriptions ------------------------------------------------

type notificationSubscriptionWire struct {
	ID                    string `json:"id"`
	TargetID              string `json:"target_id"`
	EventTypeID           string `json:"event_type_id,omitempty"`
	CameraID              string `json:"camera_id,omitempty"`
	MinSeverity           string `json:"min_severity,omitempty"`
	QuietHoursStartMinute int    `json:"quiet_hours_start_minute,omitempty"`
	QuietHoursEndMinute   int    `json:"quiet_hours_end_minute,omitempty"`
}

type notificationSubscriptionCreateRequest struct {
	TargetID              string `json:"target_id"`
	EventTypeID           string `json:"event_type_id"`
	CameraID              string `json:"camera_id"`
	MinSeverity           string `json:"min_severity"`
	QuietHoursStartMinute *int   `json:"quiet_hours_start_minute"`
	QuietHoursEndMinute   *int   `json:"quiet_hours_end_minute"`
}

func subToWire(s *store.NotificationSubscription) notificationSubscriptionWire {
	return notificationSubscriptionWire{
		ID: s.ID, TargetID: s.TargetID,
		EventTypeID: s.EventTypeID, CameraID: s.CameraID, MinSeverity: s.MinSeverity,
		QuietHoursStartMinute: s.QuietHoursStartMinute,
		QuietHoursEndMinute:   s.QuietHoursEndMinute,
	}
}

func (a *API) onV1NotificationSubscriptionsList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	rows, err := a.Store.NotificationSubscriptions.ListAllJoined(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]notificationSubscriptionWire, 0, len(rows))
	for _, j := range rows {
		out = append(out, subToWire(&j.NotificationSubscription))
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

func (a *API) onV1NotificationSubscriptionsCreate(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req notificationSubscriptionCreateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.TargetID == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("target_id is required"))
		return
	}
	row := &store.NotificationSubscription{
		ID: uuid.NewString(), TargetID: req.TargetID,
		EventTypeID: req.EventTypeID, CameraID: req.CameraID,
		MinSeverity: req.MinSeverity,
		QuietHoursStartMinute: -1, QuietHoursEndMinute: -1,
	}
	if req.QuietHoursStartMinute != nil {
		row.QuietHoursStartMinute = *req.QuietHoursStartMinute
	}
	if req.QuietHoursEndMinute != nil {
		row.QuietHoursEndMinute = *req.QuietHoursEndMinute
	}
	if err := a.Store.NotificationSubscriptions.Insert(ctx.Request.Context(), row); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.invalidateNotificationCache()
	a.emitMutationAudit(ctx, "notification_subscription.created", "notification_subscription", row.ID, nil)
	w := subToWire(row)
	ctx.JSON(http.StatusCreated, &w)
}

func (a *API) onV1NotificationSubscriptionsDelete(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	if err := a.Store.NotificationSubscriptions.Delete(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotificationSubscriptionNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.invalidateNotificationCache()
	a.emitMutationAudit(ctx, "notification_subscription.deleted", "notification_subscription", id, nil)
	ctx.Status(http.StatusNoContent)
}

// ----- outbox -------------------------------------------------------

type notificationOutboxWire struct {
	ID            string `json:"id"`
	TargetID      string `json:"target_id"`
	EventID       string `json:"event_id"`
	State         string `json:"state"`
	Attempts      int    `json:"attempts"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	CreatedAt     string `json:"created_at"`
	DeliveredAt   string `json:"delivered_at,omitempty"`
}

func outboxToWire(r *store.NotificationOutboxRow) notificationOutboxWire {
	w := notificationOutboxWire{
		ID: r.ID, TargetID: r.TargetID, EventID: r.EventID,
		State: r.State, Attempts: r.Attempts, LastError: r.LastError,
	}
	if !r.NextAttemptAt.IsZero() {
		w.NextAttemptAt = r.NextAttemptAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !r.CreatedAt.IsZero() {
		w.CreatedAt = r.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !r.DeliveredAt.IsZero() {
		w.DeliveredAt = r.DeliveredAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return w
}

func (a *API) onV1NotificationOutboxList(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	limit := 100
	if v := ctx.Query("limit"); v != "" {
		if n, err := parseUint(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := a.Store.NotificationOutbox.ListRecent(ctx.Request.Context(), limit)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	out := make([]notificationOutboxWire, 0, len(rows))
	for _, r := range rows {
		out = append(out, outboxToWire(r))
	}
	ctx.JSON(http.StatusOK, gin.H{"items": out})
}

func (a *API) onV1NotificationOutboxRetry(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	id := ctx.Param("id")
	if err := a.Store.NotificationOutbox.Reset(ctx.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotificationOutboxNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	a.emitMutationAudit(ctx, "notification_outbox.retried", "notification_outbox", id, nil)
	ctx.Status(http.StatusNoContent)
}

func (a *API) invalidateNotificationCache() {
	if a.NotifDispatcher != nil {
		a.NotifDispatcher.InvalidateCache()
	}
}

// parseUint is a small helper used by the outbox limit query param.
func parseUint(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
		if n > 1<<20 {
			return 0, errors.New("too large")
		}
	}
	return n, nil
}
