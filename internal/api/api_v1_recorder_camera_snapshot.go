package api //nolint:revive

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/gin-gonic/gin"
)

// onV1RecorderCameraSnapshot serves
// GET /v1/recorder/cameras/{id}/snapshot — the most recent media-pipeline
// fragment for a camera, returned as raw bytes.
//
// Placement (ADR 0009 §D6.3): no canonical Snapshot entity exists, and
// defining one would be ADR-level work; the endpoint therefore lives in
// the recorder-localized escape hatch alongside hls-muxers. Like the
// hls-muxer reads, this is recorder-internal observability — useful to
// LAN tools and operator dashboards but not promoted to a tier-shared
// canonical surface.
//
// Source: the recorder's HLS server keeps a sliding window of recent
// fragments per active camera; the most recent finalized fragment is
// the freshest frame the recorder can produce without standing up a
// new decode/encode pipeline (which would add a third-party dependency
// and touch media-pipeline territory). Per the snapshot scope decision,
// we return the raw HLS fragment with the Content-Type the muxer emits
// (typically video/mp4 for fMP4 variants, video/MP2T for MPEG-TS) — not
// a JPEG, since transcoding to JPEG would require a new image-encoder
// dependency.
//
// Resolution: same UUID-resolver pattern as
// /v1/recorder/hls-muxers/{id} — try configured paths, then runtime-
// active muxers (handles wildcard path entries like `all_others`).
//
// Response codes:
//   - 200: bytes returned, Content-Type set per muxer output.
//   - 404: camera UUID does not match any configured path or runtime
//     muxer.
//   - 503: camera resolved but the HLS muxer is not currently producing
//     media (no instance, no segments yet, or content didn't become
//     available within the snapshot deadline).
func (a *API) onV1RecorderCameraSnapshot(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	// UUID → path-name resolution: configured first, runtime fallback.
	// Mirrors api_v1_recorder_hls_muxers.go's onV1RecorderHLSMuxersGet
	// so that wildcard-only configs (e.g., `all_others`) still serve
	// snapshots for runtime-active cameras.
	pathName, ok := pathNameFromCameraID(c.Paths, cameraID)
	if !ok {
		pathName, ok = pathNameFromRuntimeHLSMuxers(a.HLSServer, cameraID)
	}
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	data, contentType, err := a.HLSServer.APIMuxerSnapshot(pathName)
	if err != nil {
		switch {
		case errors.Is(err, hls.ErrMuxerNotFound), errors.Is(err, hls.ErrMuxerNoContent):
			// Either the path resolved but no muxer is registered for it
			// (camera not actively serving HLS), or a muxer exists but
			// has no current content. Both surface as 503 — the camera
			// is known but not producing media right now. This matches
			// the snapshot scope decision: 503 with an explanatory body.
			a.writeError(ctx, http.StatusServiceUnavailable,
				fmt.Errorf("camera is not currently producing media: %w", err))
		default:
			a.writeError(ctx, http.StatusInternalServerError, err)
		}
		return
	}

	if contentType == "" {
		// gohlslib should always set Content-Type on a 200 segment
		// response, but fall back to a generic binary type rather than
		// letting gin guess from the byte content.
		contentType = "application/octet-stream"
	}

	// No caching — every call returns the freshest available fragment.
	ctx.Header("Cache-Control", "no-store")
	ctx.Data(http.StatusOK, contentType, data)
}
