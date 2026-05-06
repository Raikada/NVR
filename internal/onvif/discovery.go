// WS-Discovery client. Sends a multicast SOAP-over-UDP Probe to
// 239.255.255.250:3702 from each non-loopback IPv4 interface and
// listens for ProbeMatch responses. Camera replies carry an XAddrs
// field (the device service URL) and a Scopes field encoding
// vendor/model/profile metadata.
//
// The implementation is deliberately small. Discovery is a one-shot
// best-effort operation: we send the probe once on each interface,
// listen for `timeout` on a single shared UDP socket bound to a
// random port, and dedupe responses by EndpointReference UUID.
//
// We don't implement the full DPWS Resolve / Hello / Bye lifecycle —
// the recorder only needs the on-demand "show me the cameras on this
// LAN" surface. Re-running Probe on demand is the operator-driven UX.

package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DefaultDiscoveryTimeout is the wall-clock window during which we
// listen for ProbeMatch responses. ONVIF cameras typically respond
// within 1-2 seconds; 3 seconds covers the slow tail without making
// the operator wait too long.
const DefaultDiscoveryTimeout = 3 * time.Second

// MulticastAddress is the IPv4 address WS-Discovery uses for ONVIF
// device probes. RFC 5222 / DPWS / OASIS WS-Discovery define this as
// the standard discovery group; ONVIF cameras listen on it by default.
const MulticastAddress = "239.255.255.250:3702"

// DiscoveredDevice is one ProbeMatch response from a camera on the LAN.
//
// XAddr is the device service URL we'll POST GetDeviceInformation
// against. Scopes is the raw scope list (space-separated URIs); the
// caller may parse it for vendor / model / profile hints. EndpointRef
// is the camera's stable WS-Addressing identifier — used for dedup
// when a camera replies more than once.
type DiscoveredDevice struct {
	XAddr        string   `json:"xaddr"`
	EndpointRef  string   `json:"endpoint_reference"`
	Types        []string `json:"types"`
	Scopes       []string `json:"scopes"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	Hardware     string   `json:"hardware,omitempty"`
	Name         string   `json:"name,omitempty"`
	Country      string   `json:"country,omitempty"`
}

// probeEnvelope builds the WS-Discovery Probe SOAP envelope. messageID
// must be unique per probe; ONVIF cameras dedupe using it.
func probeEnvelope(messageID string) []byte {
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:a="` + NSAddressing + `"` +
		` xmlns:d="` + NSDiscovery + `"` +
		` xmlns:dn="` + NSNetwork + `"` +
		` xmlns:tds="` + NSDevice + `">` +
		`<s:Header>` +
		`<a:MessageID>` + xmlEscape(messageID) + `</a:MessageID>` +
		`<a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To>` +
		`<a:Action>` + ActionProbe + `</a:Action>` +
		`</s:Header>` +
		`<s:Body>` +
		`<d:Probe>` +
		`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types>` +
		`<d:Scopes/>` +
		`</d:Probe>` +
		`</s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

// probeMatchEnvelope is the parsed shape of a ProbeMatches response.
// We grab everything we need in one Unmarshal pass; mismatched
// element-namespace prefixes (different cameras use different
// abbreviations) are tolerated by xml.Decoder's namespace-aware
// matching.
type probeMatchEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		ProbeMatches struct {
			ProbeMatch []struct {
				EndpointReference struct {
					Address string `xml:"Address"`
				} `xml:"EndpointReference"`
				Types  string `xml:"Types"`
				Scopes string `xml:"Scopes"`
				XAddrs string `xml:"XAddrs"`
			} `xml:"ProbeMatch"`
		} `xml:"ProbeMatches"`
	} `xml:"Body"`
}

// Discoverer sends a WS-Discovery probe and collects responses for a
// fixed window. Callers wire up logger.Writer at construction so probe
// failures surface in the recorder's normal logging stream.
type Discoverer struct {
	// Timeout is the listen window after the probe is sent. Zero =
	// DefaultDiscoveryTimeout.
	Timeout time.Duration

	// Now is a swappable time source for tests. Production = time.Now.
	Now func() time.Time

	// NewMessageID is a swappable UUID source for tests. Production =
	// "uuid:" + new UUID v4.
	NewMessageID func() string
}

// Run runs one probe cycle. It returns deduped responses keyed by
// EndpointReference (or XAddr when EndpointReference is empty).
//
// Cancellation: the supplied context bounds the operation; on
// context-cancellation the listener returns the responses received so
// far without raising an error.
func (d *Discoverer) Run(ctx context.Context) ([]DiscoveredDevice, error) {
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultDiscoveryTimeout
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	newID := d.NewMessageID
	if newID == nil {
		newID = func() string { return "uuid:" + uuid.New().String() }
	}

	// Bind a UDP socket on a free port (Listen with port 0). We send
	// the probe from this socket so the camera's unicast ProbeMatch
	// reply arrives at the same socket — multicast joining isn't
	// strictly required for discovery, but we do bind a separate
	// per-interface socket below to send the multicast.
	listenConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("listen UDP: %w", err)
	}
	defer listenConn.Close()

	multicastAddr, err := net.ResolveUDPAddr("udp4", MulticastAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve multicast: %w", err)
	}

	messageID := newID()
	envelope := probeEnvelope(messageID)

	// Send from the listen socket (works for most LANs and avoids
	// per-interface socket bookkeeping). For LANs with multiple NICs
	// where the OS routing picks the wrong interface, the operator can
	// re-run the probe; on most home/office networks the default
	// outbound interface is the one cameras live on.
	if _, err := listenConn.WriteToUDP(envelope, multicastAddr); err != nil {
		return nil, fmt.Errorf("send probe: %w", err)
	}

	// Also try sending the probe out every non-loopback IPv4
	// interface explicitly. Best-effort: a failure here doesn't fail
	// the run, since the listen-socket multicast send above usually
	// covers the common case. Per-interface sends help on multi-homed
	// hosts and macOS-style configurations where the kernel default
	// route omits a stub LAN.
	d.fanOutPerInterface(envelope, multicastAddr)

	deadline := now().Add(timeout)
	if err := listenConn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	results := make(map[string]DiscoveredDevice)
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			return collectResults(results), nil
		default:
		}

		n, _, readErr := listenConn.ReadFromUDP(buf)
		if readErr != nil {
			// Deadline expired (or socket closed): we're done.
			if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
				return collectResults(results), nil
			}
			return collectResults(results), fmt.Errorf("read UDP: %w", readErr)
		}
		dev, ok := parseProbeMatch(buf[:n])
		if !ok {
			continue
		}
		key := dev.EndpointRef
		if key == "" {
			key = dev.XAddr
		}
		if key == "" {
			continue
		}
		// Dedup; keep the earliest response (cameras sometimes
		// retransmit).
		if _, exists := results[key]; !exists {
			results[key] = dev
		}

		// Soft cap: 64 unique devices is plenty for any LAN we'd run
		// the recorder against, and prevents pathological multicast
		// floods from blowing memory.
		if len(results) >= 64 {
			return collectResults(results), nil
		}
	}
}

// fanOutPerInterface sends the probe envelope from each non-loopback
// IPv4 interface. Each socket is closed before returning. Send errors
// are silently ignored — discovery is best-effort.
func (d *Discoverer) fanOutPerInterface(envelope []byte, multicastAddr *net.UDPAddr) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 ||
			iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var ip net.IP
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if v4 := ipNet.IP.To4(); v4 != nil && !v4.IsLoopback() {
					ip = v4
					break
				}
			}
		}
		if ip == nil {
			continue
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 0})
		if err != nil {
			continue
		}
		_, _ = conn.WriteToUDP(envelope, multicastAddr)
		_ = conn.Close()
	}
}

func collectResults(m map[string]DiscoveredDevice) []DiscoveredDevice {
	out := make([]DiscoveredDevice, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	return out
}

// parseProbeMatch parses a single SOAP-encoded ProbeMatches body. Returns
// the first ProbeMatch entry (cameras typically reply with one).
//
// The Scopes field encodes vendor / model / profile / hardware as a list
// of URIs in the onvif://www.onvif.org/... namespace. We pull the
// well-known fields out into named columns for the API response;
// callers that need raw scope strings get them via DiscoveredDevice.Scopes.
func parseProbeMatch(body []byte) (DiscoveredDevice, bool) {
	var env probeMatchEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return DiscoveredDevice{}, false
	}
	if len(env.Body.ProbeMatches.ProbeMatch) == 0 {
		return DiscoveredDevice{}, false
	}
	pm := env.Body.ProbeMatches.ProbeMatch[0]
	dev := DiscoveredDevice{
		EndpointRef: strings.TrimSpace(pm.EndpointReference.Address),
		Types:       splitWhitespace(pm.Types),
		Scopes:      splitWhitespace(pm.Scopes),
	}
	// XAddrs is a space-separated list. Pick the first http(s) URL.
	for _, addr := range splitWhitespace(pm.XAddrs) {
		if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
			dev.XAddr = addr
			break
		}
	}
	parseONVIFScopes(&dev)
	return dev, true
}

// splitWhitespace splits on any unicode whitespace and drops empties.
// Used for the WS-Discovery Types / Scopes / XAddrs fields, all of
// which are space-separated lists in the wire format.
func splitWhitespace(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseONVIFScopes extracts well-known fields (manufacturer, model,
// hardware, name, country) from the ONVIF scope URIs.
//
// Standard scope URIs (from the ONVIF Core Specification §5.4.1.5):
//
//	onvif://www.onvif.org/Profile/Streaming
//	onvif://www.onvif.org/type/Network_Video_Transmitter
//	onvif://www.onvif.org/hardware/HDIP-204
//	onvif://www.onvif.org/name/HikvisionCam
//	onvif://www.onvif.org/location/country/US
//
// Some cameras add vendor-specific scopes (manufacturer/Hikvision etc.);
// we accept both the canonical and vendor-extension shapes.
func parseONVIFScopes(dev *DiscoveredDevice) {
	const prefix = "onvif://www.onvif.org/"
	for _, raw := range dev.Scopes {
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		rest := raw[len(prefix):]
		// rest is "category/value..." or "category/sub/value".
		switch {
		case strings.HasPrefix(rest, "name/"):
			dev.Name = unescapeScopeValue(strings.TrimPrefix(rest, "name/"))
		case strings.HasPrefix(rest, "hardware/"):
			dev.Hardware = unescapeScopeValue(strings.TrimPrefix(rest, "hardware/"))
		case strings.HasPrefix(rest, "manufacturer/"):
			dev.Manufacturer = unescapeScopeValue(strings.TrimPrefix(rest, "manufacturer/"))
		case strings.HasPrefix(rest, "model/"):
			dev.Model = unescapeScopeValue(strings.TrimPrefix(rest, "model/"))
		case strings.HasPrefix(rest, "location/country/"):
			dev.Country = unescapeScopeValue(strings.TrimPrefix(rest, "location/country/"))
		case strings.HasPrefix(rest, "type/"):
			// type/Network_Video_Transmitter etc. — captured in Types
			// field above but kept in raw scopes too.
		}
	}
}

// unescapeScopeValue applies URL-style %XX decoding to a scope value.
// ONVIF scope URIs encode whitespace and other non-URI chars as %XX so
// "Vendor Name" becomes "Vendor%20Name" on the wire.
func unescapeScopeValue(s string) string {
	out, err := percentDecode(s)
	if err != nil {
		return s
	}
	return out
}

// percentDecode decodes a string with percent-encoded bytes (%XX). Used
// instead of net/url.QueryUnescape to avoid '+' decoding to space.
func percentDecode(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := hexNibble(s[i+1])
			lo, ok2 := hexNibble(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(byte(hi<<4) | byte(lo))
				i += 2
				continue
			}
			return s, fmt.Errorf("invalid percent escape at %d", i)
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func hexNibble(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}
