package api //nolint:revive

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/gin-gonic/gin"
)

// onV1RecorderHLSMuxersList serves /v1/recorder/hls-muxers — the
// per-camera HLS muxer state. Recorder-internal observability; not
// platform-canonical because muxer state is server-implementation-
// specific (would have to be distorted to fit a generic Stream-or-
// segment view) and the MS / Cloud tier doesn't need to project it.
//
// Rationale (D6.3): HLS muxer state describes how the recorder's HLS
// server has chosen to slice and serve a single camera's output. It
// changes whenever the muxer rebuilds (consumer (re)connect, codec
// switch, segment rotation) and is meaningful only to operators
// debugging the local HLS server. There is no canonical entity it
// belongs to: Stream models a session, Recording models a stored span,
// neither captures the muxer's transient internal state. Promoting it
// to canonical would force the MS to model HLS-server internals.
func (a *API) onV1RecorderHLSMuxersList(ctx *gin.Context) {
	data, err := a.HLSServer.APIMuxersList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	pageCount, err := paginate(&data.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	data.PageCount = pageCount

	tenantID := a.tenantID()
	for i := range data.Items {
		data.Items[i].TenantID = tenantID
	}

	ctx.JSON(http.StatusOK, data)
}

// onV1RecorderHLSMuxersGet serves /v1/recorder/hls-muxers/{id}. The
// {id} segment is a canonical Camera UUID (per ADR 0009 §D5: the
// canonical surface uses UUIDs throughout, including escape-hatch
// endpoints that key on a camera). The legacy /v3/hlsmuxers/get/*name
// keyed off the MediaMTX path-name string; this handler resolves the
// UUID back to a path-name via cameraIDFromPathName so the underlying
// hls.Server.APIMuxersGet (which still indexes by path-name internally)
// keeps working.
//
// Rationale (D6.3): same as the list handler — HLS muxer state is
// recorder-internal observability with no canonical entity.
func (a *API) onV1RecorderHLSMuxersGet(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	// Resolve the canonical UUID to a path-name. Configured paths are the
	// happy path: if the camera was created via /v1/cameras (or any
	// MediaMTX-style path config) the cameraID derived from the path-name
	// matches and pathNameFromCameraID hits.
	//
	// Wildcard config entries (e.g., `all_others`) and any path that
	// became active without a discrete conf.Path entry are not in
	// c.Paths. The HLS muxer table, however, is keyed by the runtime
	// path-name — so a muxer is producing for cam_a even though c.Paths
	// only carries `all_others`. Per the Phase 2 playback-URL convergence
	// (api_v1_recordings.go's runtime-aware resolution), fall back to the
	// runtime muxer list and match by deriving cameraID from each
	// muxer's path-name.
	pathName, ok := pathNameFromCameraID(c.Paths, cameraID)
	if !ok {
		pathName, ok = pathNameFromRuntimeHLSMuxers(a.HLSServer, cameraID)
	}
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	data, err := a.HLSServer.APIMuxersGet(pathName)
	if err != nil {
		if errors.Is(err, hls.ErrMuxerNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	data.TenantID = a.tenantID()

	ctx.JSON(http.StatusOK, data)
}

// pathNameFromRuntimeHLSMuxers walks the HLS server's runtime muxer
// list and returns the path-name whose deterministic cameraID matches
// the requested one. Used as the fallback resolver when c.Paths
// doesn't contain a discrete entry (typical for wildcard path configs
// like `all_others` that match many concrete on-disk paths). Returns
// "" / false if no muxer's path-name derives the requested cameraID.
func pathNameFromRuntimeHLSMuxers(srv defs.APIHLSServer, cameraID string) (string, bool) {
	if srv == nil {
		return "", false
	}
	list, err := srv.APIMuxersList()
	if err != nil || list == nil {
		return "", false
	}
	for _, m := range list.Items {
		if cameraIDFromPathName(m.Path) == cameraID {
			return m.Path, true
		}
	}
	return "", false
}
