// /v1/diagnostics/* — cross-platform diagnostic test runners for
// the configuration UI's Diagnostics page.
//
// Each test is a self-contained POST handler that returns once the
// test finishes. The UI runs them sequentially and streams the
// results into its console panel.
//
// All tests are stdlib-based or gopsutil-based — no shell-outs to
// `ping`, `iperf3`, etc. — so they work identically on Linux,
// macOS, and Windows. The trade-off is that they measure
// recorder-internal probes rather than calling out to OS tools the
// operator might be familiar with; the values are still useful
// (TCP RTT, NTP drift, RTSP handshake latency) and they're real
// measurements rather than canned output.

package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

/* ---------- ping (TCP-handshake) ---------- */

type v1PingRequest struct {
	Target string `json:"target"`
	Count  int    `json:"count"`
}

type v1PingSample struct {
	Seq       int    `json:"seq"`
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Reason    string `json:"reason,omitempty"`
}

type v1PingResponse struct {
	Target   string         `json:"target"`
	Samples  []v1PingSample `json:"samples"`
	AvgMs    float64        `json:"avg_ms"`
	LossPct  float64        `json:"loss_pct"`
}

func (a *API) onV1DiagnosticsPing(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	var req v1PingRequest
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := jsonUnmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Target == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("target is required"))
		return
	}
	count := req.Count
	if count <= 0 || count > 20 {
		count = 4
	}

	host, port, err := hostPortOrDefault(req.Target, 443)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	resp := v1PingResponse{Target: net.JoinHostPort(host, fmt.Sprintf("%d", port))}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	var totalMs int64
	var ok int
	for i := 0; i < count; i++ {
		dialCtx, cancel := context.WithTimeout(ctx.Request.Context(), 2*time.Second)
		start := time.Now()
		conn, derr := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		latency := time.Since(start).Milliseconds()
		cancel()
		s := v1PingSample{Seq: i + 1, LatencyMs: latency}
		if derr != nil {
			s.OK = false
			s.Reason = derr.Error()
		} else {
			s.OK = true
			ok++
			totalMs += latency
			_ = conn.Close()
		}
		resp.Samples = append(resp.Samples, s)
		// Small inter-probe pause so we don't hammer the host.
		if i+1 < count {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if ok > 0 {
		resp.AvgMs = float64(totalMs) / float64(ok)
	}
	resp.LossPct = float64(count-ok) / float64(count) * 100.0
	ctx.JSON(http.StatusOK, &resp)
}

/* ---------- ntp (drift query) ---------- */

type v1NtpRequest struct {
	Server string `json:"server"`
}

type v1NtpResponse struct {
	Server   string  `json:"server"`
	OK       bool    `json:"ok"`
	OffsetMs float64 `json:"offset_ms"`
	Reason   string  `json:"reason,omitempty"`
}

func (a *API) onV1DiagnosticsNTP(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	var req v1NtpRequest
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := jsonUnmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.Server == "" {
		req.Server = "pool.ntp.org"
	}

	offset, err := queryNTP(req.Server)
	if err != nil {
		ctx.JSON(http.StatusOK, &v1NtpResponse{Server: req.Server, OK: false, Reason: err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, &v1NtpResponse{
		Server:   req.Server,
		OK:       true,
		OffsetMs: float64(offset.Microseconds()) / 1000.0,
	})
}

// queryNTP sends a stripped-down SNTP request (RFC 4330) to
// host:123 and returns the local-vs-server offset. Pure stdlib
// implementation — works identically on every platform. Five-
// second timeout.
func queryNTP(server string) (time.Duration, error) {
	host := server
	if !strings.Contains(server, ":") {
		host = server + ":123"
	}
	conn, err := net.DialTimeout("udp", host, 3*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	// SNTP request: 48 bytes; first byte = LI(0) + VN(4) + Mode(3=client).
	pkt := make([]byte, 48)
	pkt[0] = 0x23
	t1 := time.Now()
	if _, err := conn.Write(pkt); err != nil {
		return 0, err
	}
	if _, err := conn.Read(pkt); err != nil {
		return 0, err
	}
	t4 := time.Now()

	// Server timestamp at byte offset 40-47: 32-bit seconds since
	// 1900-01-01 + 32-bit fractional seconds.
	secs := binary.BigEndian.Uint32(pkt[40:44])
	frac := binary.BigEndian.Uint32(pkt[44:48])
	if secs == 0 && frac == 0 {
		return 0, fmt.Errorf("server returned zero timestamp")
	}
	// Convert from NTP epoch (1900) to Go's Unix epoch (1970):
	// 2208988800 seconds between them.
	const ntpEpoch = 2208988800
	serverNs := int64(secs-ntpEpoch)*int64(time.Second) + (int64(frac)*int64(time.Second))/(1<<32)
	serverTime := time.Unix(0, serverNs)
	// Offset = ((serverTime - t1) + (serverTime - t4)) / 2 — the
	// clock-skew estimate from the SNTP standard.
	offset := (serverTime.Sub(t1) + serverTime.Sub(t4)) / 2
	return offset, nil
}

/* ---------- rtsp-probe (probe all configured cameras) ---------- */

type v1RTSPProbeResult struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	LatencyMs int64  `json:"latency_ms"`
	Reason    string `json:"reason,omitempty"`
}

type v1RTSPProbeResponse struct {
	Results []v1RTSPProbeResult `json:"results"`
	OKCount int                 `json:"ok_count"`
	Total   int                 `json:"total"`
}

func (a *API) onV1DiagnosticsRTSPProbe(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()
	if c == nil {
		a.writeError(ctx, http.StatusInternalServerError,
			fmt.Errorf("recorder configuration not loaded"))
		return
	}

	resp := v1RTSPProbeResponse{}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	for name, p := range c.Paths {
		if p == nil || p.Source == "" || p.Source == "publisher" {
			continue
		}
		host, port, err := hostPortFromSourceURL(p.Source)
		r := v1RTSPProbeResult{Path: name, URL: redactedSource(p.Source)}
		resp.Total++
		if err != nil {
			r.Reachable = false
			r.Reason = err.Error()
			resp.Results = append(resp.Results, r)
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx.Request.Context(), 3*time.Second)
		start := time.Now()
		conn, derr := dialer.DialContext(dialCtx, "tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		r.LatencyMs = time.Since(start).Milliseconds()
		cancel()
		if derr != nil {
			r.Reachable = false
			r.Reason = derr.Error()
		} else {
			r.Reachable = true
			resp.OKCount++
			_ = conn.Close()
		}
		resp.Results = append(resp.Results, r)
	}
	ctx.JSON(http.StatusOK, &resp)
}

// redactedSource strips userinfo from a URL for safe logging.
func redactedSource(s string) string {
	if i := strings.Index(s, "@"); i > 0 {
		// Find the scheme:// boundary so we keep the scheme intact.
		if j := strings.Index(s, "://"); j > 0 && j < i {
			return s[:j+3] + "redacted@" + s[i+1:]
		}
	}
	return s
}

/* ---------- shared helpers ---------- */

func hostPortOrDefault(target string, defaultPort int) (string, int, error) {
	if !strings.Contains(target, "://") {
		// Bare host or host:port.
		if h, p, err := net.SplitHostPort(target); err == nil {
			port, perr := strconv.Atoi(p)
			if perr != nil {
				return "", 0, fmt.Errorf("invalid port: %w", perr)
			}
			return h, port, nil
		}
		return target, defaultPort, nil
	}
	return hostPortFromSourceURL(target)
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
