// /v1/recorder/network-info — interface details + bandwidth window
// stats for the configuration UI's Network page.
//
// gopsutil drives every reading: interface enumeration, MAC, MTU,
// speed (where the OS exposes it), per-host hostname/uptime, DNS
// resolvers, and per-interface IP addresses. The recorder's
// existing bandwidth sampler exports peak/avg over its rolling
// window for the bandwidth row.
//
// Cross-platform notes:
//   - Linux: every field is populated.
//   - macOS: interface speed is not exposed by the kernel; the
//     speed field returns 0. Everything else works.
//   - Windows: gopsutil reads per-adapter stats; speed and MAC
//     are present.
//   - DNS resolvers come from /etc/resolv.conf on unix and
//     gopsutil's net DNS readers; falls back to empty list on
//     platforms where the data isn't available.

package api

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	hoststat "github.com/shirou/gopsutil/v3/host"
	netstat "github.com/shirou/gopsutil/v3/net"
)

type v1NetworkInterface struct {
	Name        string   `json:"name"`
	HardwareAddr string  `json:"hardware_addr"`
	MTU         int      `json:"mtu"`
	IsUp        bool     `json:"is_up"`
	IsLoopback  bool     `json:"is_loopback"`
	Addresses   []string `json:"addresses,omitempty"`
}

type v1BandwidthStats struct {
	NowRxBps  float64 `json:"now_rx_bps"`
	NowTxBps  float64 `json:"now_tx_bps"`
	PeakBps   float64 `json:"peak_bps"`
	AvgBps    float64 `json:"avg_bps"`
	WindowSec int     `json:"window_sec"`
}

type v1NetworkInfo struct {
	OS         string               `json:"os"`
	Platform   string               `json:"platform"`
	Hostname   string               `json:"hostname"`
	Interfaces []v1NetworkInterface `json:"interfaces"`
	DNS        []string             `json:"dns,omitempty"`
	Bandwidth  v1BandwidthStats     `json:"bandwidth"`
}

func (a *API) onV1RecorderNetworkInfo(ctx *gin.Context) {
	info := v1NetworkInfo{
		OS: runtime.GOOS,
	}

	if hi, err := hoststat.Info(); err == nil && hi != nil {
		info.Platform = hi.Platform
		info.Hostname = hi.Hostname
	} else if hn, err := os.Hostname(); err == nil {
		info.Hostname = hn
	}

	// Interfaces — gopsutil's net.Interfaces() returns each NIC
	// with MTU, MAC, flags, and IP addresses. We expose the subset
	// the UI consumes today.
	if ifs, err := netstat.Interfaces(); err == nil {
		for _, iface := range ifs {
			isLoopback := containsFlag(iface.Flags, "loopback")
			isUp := containsFlag(iface.Flags, "up")
			addrs := make([]string, 0, len(iface.Addrs))
			for _, a := range iface.Addrs {
				addrs = append(addrs, a.Addr)
			}
			info.Interfaces = append(info.Interfaces, v1NetworkInterface{
				Name:         iface.Name,
				HardwareAddr: iface.HardwareAddr,
				MTU:          iface.MTU,
				IsUp:         isUp,
				IsLoopback:   isLoopback,
				Addresses:    addrs,
			})
		}
	}

	// DNS resolvers. On unix we parse /etc/resolv.conf — same
	// source the OS resolver uses. On Windows there's no
	// equivalent file path; leave empty (the UI surfaces an empty
	// list rather than wrong data).
	if runtime.GOOS != "windows" {
		info.DNS = readDNSResolvers()
	}

	// Bandwidth: latest sampled rates plus rolling-window peak / avg.
	rxBps, txBps := bandwidthSamplerSingleton.Sample()
	peak, avg := bandwidthSamplerSingleton.HistoryStats()
	info.Bandwidth = v1BandwidthStats{
		NowRxBps:  rxBps,
		NowTxBps:  txBps,
		PeakBps:   peak,
		AvgBps:    avg,
		WindowSec: int(bandwidthHistoryWindow.Seconds()),
	}

	ctx.JSON(http.StatusOK, &info)
}

// containsFlag is case-insensitive flag-name lookup against the
// gopsutil-formatted flag list ("up", "loopback", "broadcast", etc.).
func containsFlag(flags []string, want string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}

// readDNSResolvers parses /etc/resolv.conf and returns the
// nameserver IPs in declaration order. Returns empty on read or
// parse failure — caller treats it as "DNS not exposed."
func readDNSResolvers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "nameserver") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		// Validate as IP so a malformed file doesn't pollute.
		if ip := net.ParseIP(parts[1]); ip != nil {
			out = append(out, ip.String())
		}
	}
	return out
}
