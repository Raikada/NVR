// POST /v1/cameras/probe — pre-flight reachability check for the
// manual-add wizard's "Probe Device" / "Test Stream" buttons.
//
// Takes a source URL (rtsp://, rtmp://, http://, srt://, etc.) plus
// optional credentials, attempts a TCP dial against host:port, and
// returns reachability + handshake-latency results. Doesn't open
// the actual stream — that requires consuming a license, picking a
// codec, and starting a real recorder pipeline; for a config-UI
// pre-flight, "is the host reachable on the right port" is the
// useful question.
//
// Cross-platform via stdlib `net.Dialer` — works the same on
// Linux, macOS, and Windows.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const probeTimeout = 5 * time.Second

type v1CameraProbeRequest struct {
	SourceURL string `json:"source_url"`
}

type v1CameraProbeResponse struct {
	Reachable bool   `json:"reachable"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	LatencyMs int64  `json:"latency_ms"`
	Reason    string `json:"reason,omitempty"`
}

func (a *API) onV1CamerasProbe(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req v1CameraProbeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.SourceURL == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("source_url is required"))
		return
	}

	host, port, err := hostPortFromSourceURL(req.SourceURL)
	if err != nil {
		ctx.JSON(http.StatusOK, &v1CameraProbeResponse{
			Reachable: false,
			Reason:    err.Error(),
		})
		return
	}

	dialer := &net.Dialer{Timeout: probeTimeout}
	probeCtx, cancel := context.WithTimeout(ctx.Request.Context(), probeTimeout)
	defer cancel()

	start := time.Now()
	conn, err := dialer.DialContext(probeCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	latency := time.Since(start)

	if err != nil {
		ctx.JSON(http.StatusOK, &v1CameraProbeResponse{
			Reachable: false,
			Host:      host,
			Port:      port,
			LatencyMs: latency.Milliseconds(),
			Reason:    err.Error(),
		})
		return
	}
	_ = conn.Close()
	ctx.JSON(http.StatusOK, &v1CameraProbeResponse{
		Reachable: true,
		Host:      host,
		Port:      port,
		LatencyMs: latency.Milliseconds(),
	})
}

// hostPortFromSourceURL parses a recorder source URL and returns
// the host/port to dial. Falls back to scheme-default ports when
// the URL doesn't include one (rtsp:554, rtmp:1935, srt:9710,
// http:80, https:443, hls/rtsps as their secure counterparts).
func hostPortFromSourceURL(raw string) (string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, fmt.Errorf("invalid url: %w", err)
	}
	if u.Hostname() == "" {
		return "", 0, fmt.Errorf("url has no host")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
		return u.Hostname(), port, nil
	}
	switch strings.ToLower(u.Scheme) {
	case "rtsp":
		return u.Hostname(), 554, nil
	case "rtsps":
		return u.Hostname(), 322, nil
	case "rtmp":
		return u.Hostname(), 1935, nil
	case "rtmps":
		return u.Hostname(), 443, nil
	case "srt":
		return u.Hostname(), 9710, nil
	case "http":
		return u.Hostname(), 80, nil
	case "https":
		return u.Hostname(), 443, nil
	}
	return "", 0, fmt.Errorf("unsupported scheme %q", u.Scheme)
}
