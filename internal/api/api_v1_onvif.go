// Package api: /v1/onvif/* handlers.
//
// Five endpoints, all permission-gated per ADR 0010 D2:
//
//   POST   /v1/onvif/discover                    camera.create
//   POST   /v1/onvif/device-info                 camera.create
//   POST   /v1/onvif/event-subscriptions         event.read
//   GET    /v1/onvif/event-subscriptions         event.read
//   DELETE /v1/onvif/event-subscriptions/{id}    event.read
//
// Discovery + device-info are read-only inspection of the LAN; they
// gate on camera.create because the typical caller is the operator
// adding a new camera, and that's the same right gate that protects
// /v1/cameras/probe (the manual-add wizard's reachability test).
//
// Event subscriptions gate on event.read: subscribing to an ONVIF
// camera is a "read events from this camera" operation. Mutation gates
// (update/delete) on the canonical Camera record stay separate.
//
// Translation of ONVIF events to canonical Events flows through
// publishOnvifEvent below — the recorder's standard PublishX helpers
// don't fit (the topic vocabulary is bigger than camera.online /
// camera.offline / segment.write_failed) so we go directly to the
// EventStore with a per-event mapping table from internal/onvif.

package api //nolint:revive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/onvif"
)

// onvifManager is the per-process subscription manager. Lazily
// constructed on first /v1/onvif/event-subscriptions hit. Replaceable
// in tests via SetOnvifManager.
var (
	onvifManagerMu       sync.RWMutex
	onvifManager         *onvif.Manager
	onvifManagerOnce     sync.Once
)

// SetOnvifManager replaces the package-level manager. Used by core.go
// at startup to inject a manager wired to the same event store +
// tenant-id resolver as the rest of the pipeline. Tests call this
// directly to inject a stub manager.
func SetOnvifManager(m *onvif.Manager) {
	onvifManagerMu.Lock()
	defer onvifManagerMu.Unlock()
	onvifManager = m
}

// getOnvifManager returns the active manager, lazily constructing one
// if SetOnvifManager has not been called. The lazy fallback wires its
// sink to publishOnvifEvent against the package-wide default
// EventStore so /v1/events still surfaces events when the recorder is
// running pre-core-wired (e.g., in tests that exercise the API
// directly without core.go).
func getOnvifManager() *onvif.Manager {
	onvifManagerMu.RLock()
	m := onvifManager
	onvifManagerMu.RUnlock()
	if m != nil {
		return m
	}
	onvifManagerOnce.Do(func() {
		mgr := onvif.NewManager(nil, func(ev onvif.EventNotification) {
			publishOnvifEvent(ev)
		}, nil)
		onvifManagerMu.Lock()
		onvifManager = mgr
		onvifManagerMu.Unlock()
	})
	onvifManagerMu.RLock()
	defer onvifManagerMu.RUnlock()
	return onvifManager
}

// PublishOnvifEvent is the exported alias used by core.go to wire the
// subscription manager's sink to the pipeline's EventStore at startup.
// It calls publishOnvifEvent below, which holds the actual translation
// logic.
func PublishOnvifEvent(ev onvif.EventNotification) {
	publishOnvifEvent(ev)
}

// publishOnvifEvent translates an ONVIF NotificationMessage to a
// canonical Event and publishes it through the same pipeline that
// camera.online / camera.offline use.
//
// The event's subject_kind is camera; subject_id is the operator-
// supplied camera id (carried through Subscription.CameraID and into
// EventNotification.SourceCameraID by the manager's run loop).
//
// Unknown topics fall through to camera.onvif_event with the raw topic
// in attributes.
func publishOnvifEvent(ev onvif.EventNotification) {
	store, tenantID := pipelineTarget()
	if store == nil {
		return
	}
	mapping, mapped := canonicalEventKindFor(ev.Topic)
	kind := mapping.Kind
	severity := mapping.Severity
	message := mapping.Message
	if !mapped {
		kind = "camera.onvif_event"
		severity = defs.EventSeverityInfo
		message = "ONVIF event received"
	}

	attrs := map[string]string{
		"onvif_topic": ev.Topic,
	}
	if ev.PropertyOper != "" {
		attrs["onvif_property_operation"] = ev.PropertyOper
	}
	for k, v := range ev.Data {
		attrs[k] = v
	}
	occurredAt := ev.UTCTime
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	var corr *string
	if ev.MessageID != "" {
		c := ev.MessageID
		corr = &c
	}

	store.Publish(defs.EventInput{
		Kind:          kind,
		Severity:      severity,
		SubjectKind:   defs.EventSubjectKindCamera,
		SubjectID:     ev.SourceCameraID,
		Message:       message,
		Attributes:    attrs,
		OccurredAt:    occurredAt,
		CorrelationID: corr,
	}, "", tenantID, "")
}

// canonicalEventKindFor wraps the package-private mapping table so it
// can be called from internal/api. Re-exported via the onvif package
// would invert the dependency direction; a small adapter is cleaner.
func canonicalEventKindFor(topic string) (kindMapping, bool) {
	mapping, ok := onvif.CanonicalEventKindForTopic(topic)
	return kindMapping{
		Kind:     mapping.Kind,
		Severity: mapping.Severity,
		Message:  mapping.Message,
	}, ok
}

type kindMapping struct {
	Kind     string
	Severity defs.EventSeverity
	Message  string
}

// onvifDiscoverRequest is the body shape for POST /v1/onvif/discover.
type onvifDiscoverRequest struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// onvifDiscoverResponse is the response shape for POST /v1/onvif/discover.
type onvifDiscoverResponse struct {
	Devices []onvif.DiscoveredDevice `json:"devices"`
}

func (a *API) onV1OnvifDiscover(ctx *gin.Context) {
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req onvifDiscoverRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			a.writeError(ctx, http.StatusBadRequest, err)
			return
		}
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = onvif.DefaultDiscoveryTimeout
	}
	if timeout > 15*time.Second {
		timeout = 15 * time.Second
	}

	d := &onvif.Discoverer{Timeout: timeout}
	rctx, cancel := context.WithTimeout(ctx.Request.Context(), timeout+2*time.Second)
	defer cancel()
	devices, err := d.Run(rctx)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("discover: %w", err))
		return
	}
	if devices == nil {
		devices = []onvif.DiscoveredDevice{}
	}
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].XAddr < devices[j].XAddr
	})
	ctx.JSON(http.StatusOK, &onvifDiscoverResponse{Devices: devices})
}

// onvifDeviceInfoRequest is the body for POST /v1/onvif/device-info.
type onvifDeviceInfoRequest struct {
	XAddr    string `json:"xaddr"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (a *API) onV1OnvifDeviceInfo(ctx *gin.Context) {
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req onvifDeviceInfoRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.XAddr == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("xaddr is required"))
		return
	}
	rctx, cancel := context.WithTimeout(ctx.Request.Context(), onvif.DefaultDeviceInfoTimeout+2*time.Second)
	defer cancel()
	c := &onvif.DeviceClient{
		XAddr:    req.XAddr,
		Username: req.Username,
		Password: req.Password,
	}
	info, err := c.GetDeviceInformation(rctx)
	if err != nil {
		// Auth failures and SOAP faults surface as 400s so the SPA can
		// distinguish "credentials needed" from "recorder broken".
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	ctx.JSON(http.StatusOK, &info)
}

// onvifEventSubscriptionRequest is the body for POST /v1/onvif/event-subscriptions.
type onvifEventSubscriptionRequest struct {
	CameraID string `json:"camera_id"`
	XAddr    string `json:"xaddr"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (a *API) onV1OnvifEventSubscriptionsPost(ctx *gin.Context) {
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req onvifEventSubscriptionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.CameraID == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("camera_id is required"))
		return
	}
	if _, err := validateCameraID(req.CameraID); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera_id: %w", err))
		return
	}
	if req.XAddr == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("xaddr is required"))
		return
	}
	rctx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
	defer cancel()
	rec, err := getOnvifManager().AddSubscription(rctx, onvif.AddSubscriptionInput{
		CameraID: req.CameraID,
		XAddr:    req.XAddr,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	ctx.JSON(http.StatusCreated, rec)
}

// onvifEventSubscriptionsList is the response shape for the LIST
// endpoint.
type onvifEventSubscriptionsList struct {
	Items []onvif.SubscriptionRecord `json:"items"`
}

func (a *API) onV1OnvifEventSubscriptionsList(ctx *gin.Context) {
	items := getOnvifManager().List()
	if items == nil {
		items = []onvif.SubscriptionRecord{}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	ctx.JSON(http.StatusOK, &onvifEventSubscriptionsList{Items: items})
}

func (a *API) onV1OnvifEventSubscriptionsDelete(ctx *gin.Context) {
	id := ctx.Param("id")
	if id == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("id is required"))
		return
	}
	rctx, cancel := context.WithTimeout(ctx.Request.Context(), 15*time.Second)
	defer cancel()
	if err := getOnvifManager().RemoveSubscription(rctx, id); err != nil {
		a.writeError(ctx, http.StatusNotFound, err)
		return
	}
	a.writeOK(ctx)
}
