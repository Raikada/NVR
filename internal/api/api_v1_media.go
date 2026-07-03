// Package api: SP4 — signed media endpoints, DB-backed event wire
// enrichment, and event-to-clip.
//
//	GET  /v1/media/snapshots/:event_id/:kind?exp&sig   anonymous-with-signature
//	GET  /v1/media/clips/:clip_id?exp&sig              anonymous-with-signature
//	POST /v1/events/:id/clip                            (clip.create)
//
// Signed URLs exist because <img> tags and webhook consumers can't
// send Authorization headers; every fetch verifies the HMAC before any
// file read, and file paths are always server-derived (event/clip ids,
// never client paths).
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/rbac"
)

const (
	mediaURLTTL = 15 * time.Minute
	// defaultClipRollSeconds pads event clips on both sides when the
	// request doesn't specify.
	defaultClipRollSeconds = 5
	maxClipRollSeconds     = 300
)

func (a *API) registerV1Media(r gin.IRouter) {
	// Anonymous-with-signature: verification happens in the handlers.
	r.GET("/media/snapshots/:event_id/:kind", a.onV1MediaSnapshot)
	r.GET("/media/clips/:clip_id", a.onV1MediaClip)
	r.POST("/events/:id/clip", rbac.RequirePerm(rbac.PermClipCreate, a.auditEmitter()), a.onV1EventClipPost)
}

// signedMediaURL builds a relative signed URL for a media path.
func (a *API) signedMediaURL(path string) string {
	if a.Signer == nil {
		return ""
	}
	exp, sig := a.Signer.Sign(path, mediaURLTTL)
	return fmt.Sprintf("%s?exp=%d&sig=%s", path, exp, sig)
}

// verifySignedRequest checks exp+sig for the request path. Writes the
// error response itself when verification fails.
func (a *API) verifySignedRequest(ctx *gin.Context, path string) bool {
	if a.Signer == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("media signing not wired"))
		return false
	}
	exp, err := strconv.ParseInt(ctx.Query("exp"), 10, 64)
	if err != nil {
		a.writeError(ctx, http.StatusForbidden, errors.New("forbidden"))
		return false
	}
	if time.Now().Unix() >= exp {
		a.writeError(ctx, http.StatusForbidden, errors.New("expired"))
		return false
	}
	if !a.Signer.Verify(path, exp, ctx.Query("sig")) {
		a.writeError(ctx, http.StatusForbidden, errors.New("forbidden"))
		return false
	}
	return true
}

func (a *API) onV1MediaSnapshot(ctx *gin.Context) {
	eventID := ctx.Param("event_id")
	kind := ctx.Param("kind")
	if kind != "full" && kind != "thumb" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("kind must be full or thumb"))
		return
	}
	path := "/v1/media/snapshots/" + eventID + "/" + kind
	if !a.verifySignedRequest(ctx, path) {
		return
	}
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	row, err := a.Store.EventSnapshots.Get(ctx.Request.Context(), eventID, kind)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("snapshot not found"))
		return
	}
	ctx.Header("Cache-Control", "private, max-age=300")
	ctx.File(row.Path)
}

func (a *API) onV1MediaClip(ctx *gin.Context) {
	clipID := ctx.Param("clip_id")
	path := "/v1/media/clips/" + clipID
	if !a.verifySignedRequest(ctx, path) {
		return
	}
	c, ok := a.clipStore().Get(clipID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, errors.New("clip not found"))
		return
	}
	if c.State != defs.ClipStateReady {
		a.writeError(ctx, http.StatusConflict, fmt.Errorf("clip is not ready (state=%s)", c.State))
		return
	}
	if c.ExportPath == "" {
		a.writeError(ctx, http.StatusInternalServerError, errors.New("clip ready but export path missing"))
		return
	}
	ctx.Header("Content-Type", "video/mp4")
	ctx.Header("Content-Disposition", `attachment; filename="`+clipID+`.mp4"`)
	ctx.File(c.ExportPath)
}

type eventClipRequest struct {
	PreRollSeconds  *int `json:"pre_roll_seconds"`
	PostRollSeconds *int `json:"post_roll_seconds"`
}

// onV1EventClipPost lazily extracts a clip around an event. Idempotent:
// an existing clip for the event returns 200 with that clip.
func (a *API) onV1EventClipPost(ctx *gin.Context) {
	if a.EventsService == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("events service not wired"))
		return
	}
	eventID := ctx.Param("id")
	ev, err := a.EventsService.Get(ctx.Request.Context(), eventID)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("event not found"))
		return
	}

	// Idempotency: one clip per event.
	if existing, ok := a.clipStore().FindByEventID(eventID); ok {
		ctx.JSON(http.StatusOK, gin.H{
			"clip":         existing,
			"download_url": a.signedMediaURL("/v1/media/clips/" + existing.ID),
		})
		return
	}

	var req eventClipRequest
	_ = ctx.ShouldBindJSON(&req) // empty body = defaults
	pre := defaultClipRollSeconds
	post := defaultClipRollSeconds
	if req.PreRollSeconds != nil {
		pre = *req.PreRollSeconds
	}
	if req.PostRollSeconds != nil {
		post = *req.PostRollSeconds
	}
	if pre < 0 || post < 0 || pre > maxClipRollSeconds || post > maxClipRollSeconds {
		a.writeError(ctx, http.StatusBadRequest,
			fmt.Errorf("pre/post roll must be 0..%d seconds", maxClipRollSeconds))
		return
	}

	start := ev.OccurredAt.Add(-time.Duration(pre) * time.Second)
	end := ev.OccurredAt.Add(time.Duration(post) * time.Second)
	label := fmt.Sprintf("event-%s-%s", ev.TypeID, ev.OccurredAt.UTC().Format("20060102-150405"))

	clip, err := a.createClip(clipCreateParams{
		CameraID:       ev.CameraID,
		Label:          label,
		RangeStartedAt: start,
		RangeEndedAt:   end,
		EventID:        eventID,
	})
	if err != nil {
		var nf *clipNoSegmentsError
		// A camera with no recordings at all resolves to "not found"
		// in the recording registry — same operator meaning here: the
		// footage for this event no longer exists (or never did).
		if errors.As(err, &nf) || errors.Is(err, errClipCameraNotFound) {
			a.writeError(ctx, http.StatusConflict,
				errors.New("no recording segments cover the event window (already swept?)"))
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	a.emitMutationAudit(ctx, "clip.created_from_event", "clip", clip.ID, map[string]string{
		"event_id": eventID, "camera_id": ev.CameraID,
	})
	ctx.JSON(http.StatusCreated, gin.H{
		"clip":         clip,
		"download_url": a.signedMediaURL("/v1/media/clips/" + clip.ID),
	})
}

// GrabLiveJPEG captures a JPEG from a camera's live HLS muxer — the
// snapshots service's ladder fallback (no camera round-trip).
func (a *API) GrabLiveJPEG(ctx context.Context, pathName string) ([]byte, error) {
	if a.HLSServer == nil || interfaceIsEmpty(a.HLSServer) {
		return nil, errors.New("hls server not available")
	}
	data, _, err := a.HLSServer.APIMuxerSnapshot(pathName)
	if err != nil {
		return nil, err
	}
	return snapshotJPEGFromFragment(ctx, data)
}
