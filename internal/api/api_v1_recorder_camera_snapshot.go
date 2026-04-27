package api //nolint:revive

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
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
// the freshest frame the recorder can produce. The handler then runs
// it through the in-process libav (cgo) snapshot path to emit a JPEG
// keyframe. When the conversion fails (corrupt fragment, unsupported
// codec, etc.) the handler falls back to the raw HLS fragment with
// the muxer's content type so the endpoint stays useful for
// diagnostics.
//
// Resolution: same UUID-resolver pattern as
// /v1/recorder/hls-muxers/{id} — try configured paths, then runtime-
// active muxers (handles wildcard path entries like `all_others`).
//
// Optional query parameters:
//   - at=<RFC3339 timestamp>: when set, the snapshot is taken from
//     the recorded segment that covers that point in history rather
//     than from the live HLS muxer. The timestamp is parsed as
//     RFC3339Nano (with RFC3339 fallback). Resolution: locate the
//     recordstore segment whose [start, next-start) interval contains
//     the timestamp; open the segment file; pass to the same libav
//     decode/encode helper used by the live path. 404 if no segment
//     covers the timestamp; 400 on parse failure.
//
// Response codes:
//   - 200: bytes returned, Content-Type set per muxer output.
//   - 400: invalid camera id or invalid `at` timestamp.
//   - 404: camera UUID does not match any configured path or runtime
//     muxer; or `at=` is set but no recorded segment covers that
//     timestamp.
//   - 503: camera resolved but the HLS muxer is not currently producing
//     media (no instance, no segments yet, or content didn't become
//     available within the snapshot deadline). Live path only.
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
	// See resolveCameraPath; the runtime fallback ensures snapshots work
	// for cameras served by wildcard-only configs (e.g., `all_others`).
	pathName, ok := resolveCameraPath(c, a.HLSServer, cameraID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// `?at=` branches to the recorded-history path. The live-muxer
	// flow below stays exactly as-is when the parameter is absent.
	if atRaw := ctx.Query("at"); atRaw != "" {
		atTime, perr := parseAtTimestamp(atRaw)
		if perr != nil {
			a.writeError(ctx, http.StatusBadRequest,
				fmt.Errorf("invalid 'at' parameter: %w", perr))
			return
		}
		a.serveRecordedSnapshot(ctx, cameraID, pathName, c, atTime)
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

	// Convert the fragment to JPEG via in-process libav (cgo). The
	// success path is the only happy outcome: an image/jpeg body with
	// X-Snapshot-Format: jpeg. On any decode/encode failure (corrupt
	// fragment, codec the recorder's libav build doesn't support, frame
	// with weird dimensions, etc.) we log the underlying error and
	// return the raw fragment bytes with X-Snapshot-Format:
	// hls-fragment-fallback — keeps the endpoint useful for diagnostic
	// "what did the muxer emit?" workflows even when conversion can't
	// produce an image.
	jpegBytes, jerr := snapshotJPEGFromFragment(ctx.Request.Context(), data)
	if jerr == nil {
		ctx.Header("X-Snapshot-Format", "jpeg")
		ctx.Data(http.StatusOK, "image/jpeg", jpegBytes)
		return
	}
	a.Log(logger.Warn, "snapshot jpeg conversion failed for camera %s: %v", cameraID, jerr)
	ctx.Header("X-Snapshot-Format", "hls-fragment-fallback")
	ctx.Data(http.StatusOK, contentType, data)
}

// parseAtTimestamp parses the `?at=` query value. Tries RFC3339Nano
// first to accept sub-second precision, then plain RFC3339 as a
// fallback for clients that don't include nanoseconds. Mirrors
// parseTimeQuery in api_v1_recordings.go.
func parseAtTimestamp(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err == nil {
		return t, nil
	}
	t, err = time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// serveRecordedSnapshot handles `?at=<rfc3339>` requests: locate the
// recordstore segment that covers the requested timestamp, read its
// bytes, and run them through the same libav decode/encode helper
// that the live-muxer path uses. Read-only against recordstore via
// the public FindSegments / FindPathConf APIs, per AGENTS.md §6.
//
// Coverage rule: the segment whose Start <= at AND whose successor's
// Start > at (or, for the most recent segment, Start <= at). Outside
// any covered interval returns 404. We intentionally do NOT fall
// back to "the closest segment" — a request for a timestamp before
// any recording or after the last segment's plausible end is a
// distinct condition from "no live media right now" and the
// resource genuinely does not exist.
func (a *API) serveRecordedSnapshot(
	ctx *gin.Context,
	cameraID string,
	pathName string,
	c *conf.Conf,
	at time.Time,
) {
	if c == nil {
		a.writeError(ctx, http.StatusNotFound,
			fmt.Errorf("no recorded segment covers the requested timestamp"))
		return
	}

	pathConf, _, err := conf.FindPathConf(c.Paths, pathName)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound,
			fmt.Errorf("no recorded segment covers the requested timestamp: %w", err))
		return
	}

	// FindSegments with start=&at returns "the segment that may
	// contain at" plus everything after it (time-sorted ascending).
	// The first entry is the candidate; if its Start is strictly
	// after `at`, the timestamp is before any recorded segment.
	segs, err := recordstore.FindSegments(pathConf, pathName, &at, nil)
	if err != nil || len(segs) == 0 {
		a.writeError(ctx, http.StatusNotFound,
			fmt.Errorf("no recorded segment covers the requested timestamp"))
		return
	}
	candidate := segs[0]
	if candidate.Start.After(at) {
		a.writeError(ctx, http.StatusNotFound,
			fmt.Errorf("no recorded segment covers the requested timestamp"))
		return
	}

	data, err := os.ReadFile(candidate.Fpath)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError,
			fmt.Errorf("reading recorded segment: %w", err))
		return
	}

	// Recordstore segments are self-contained files (each fmp4
	// segment carries its own moov; ts segments are intrinsically
	// self-contained), so unlike the live-HLS path no init-segment
	// concatenation is needed before decode.
	contentType := "video/mp4"
	if pathConf.RecordFormat == conf.RecordFormatMPEGTS {
		contentType = "video/MP2T"
	}

	ctx.Header("Cache-Control", "no-store")

	jpegBytes, jerr := snapshotJPEGFromFragment(ctx.Request.Context(), data)
	if jerr == nil {
		ctx.Header("X-Snapshot-Format", "jpeg")
		ctx.Data(http.StatusOK, "image/jpeg", jpegBytes)
		return
	}
	a.Log(logger.Warn, "recorded-snapshot jpeg conversion failed for camera %s at %s: %v",
		cameraID, at.Format(time.RFC3339Nano), jerr)
	ctx.Header("X-Snapshot-Format", "hls-fragment-fallback")
	ctx.Data(http.StatusOK, contentType, data)
}
