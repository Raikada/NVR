package defs

import (
	"time"
)

// ProtocolSpecific is the discriminated union of per-protocol runtime
// detail on a Stream, keyed by Stream.Protocol.
//
// Phase 1 of ADR 0009 only marshals these types out. JSON unmarshaling
// across the discriminator is not implemented; Phase 2 adds a custom
// UnmarshalJSON on Stream if PATCH/POST input handlers need it.
type ProtocolSpecific interface {
	protocolSpecific()
}

// RTSPTransport is the transport mode of an RTSP / RTSPS session.
type RTSPTransport string

// RTSP transport modes.
const (
	RTSPTransportTCP          RTSPTransport = "tcp"
	RTSPTransportUDP          RTSPTransport = "udp"
	RTSPTransportUDPMulticast RTSPTransport = "udp_multicast"
)

// TransportConnection describes one RTSP-level transport connection
// folded into the parent Stream's protocol_specific block. This replaces
// the standalone RTSPConn / RTSPSConn endpoints in the old API.
type TransportConnection struct {
	ID            string  `json:"id"`
	RemoteAddr    string  `json:"remote_addr"`
	Tunnel        *string `json:"tunnel,omitempty"`
	BytesInbound  int64   `json:"bytes_inbound"`
	BytesOutbound int64   `json:"bytes_outbound"`
}

// ProtocolSpecificRTSP is the RTSP runtime detail for a Stream.
//
// Used for both protocol=rtsp and protocol=rtsps (the TLS variant carries
// the same shape; the Stream.Protocol field encodes the TLS distinction).
type ProtocolSpecificRTSP struct {
	Transport            RTSPTransport         `json:"transport"`
	Profile              string                `json:"profile"`
	TransportConnections []TransportConnection `json:"transport_connections,omitempty"`

	RTPPacketsInbound       int64 `json:"rtp_packets_inbound"`
	RTPPacketsLost          int64 `json:"rtp_packets_lost"`
	RTPPacketsInError       int64 `json:"rtp_packets_in_error"`
	RTPJitter               int64 `json:"rtp_jitter"`
	RTCPPacketsInbound      int64 `json:"rtcp_packets_inbound"`
	RTCPPacketsInError      int64 `json:"rtcp_packets_in_error"`
	RTPPacketsOutbound      int64 `json:"rtp_packets_outbound"`
	RTPPacketsReportedLost  int64 `json:"rtp_packets_reported_lost"`
	RTPPacketsDiscarded     int64 `json:"rtp_packets_discarded"`
	RTCPPacketsOutbound     int64 `json:"rtcp_packets_outbound"`
}

func (*ProtocolSpecificRTSP) protocolSpecific() {}

// RTMPState is the state of an RTMP / RTMPS connection.
type RTMPState string

// RTMP states.
const (
	RTMPStateIdle    RTMPState = "idle"
	RTMPStateRead    RTMPState = "read"
	RTMPStatePublish RTMPState = "publish"
)

// ProtocolSpecificRTMP is the RTMP runtime detail for a Stream.
//
// Used for both protocol=rtmp and protocol=rtmps.
type ProtocolSpecificRTMP struct {
	State RTMPState `json:"state"`
}

func (*ProtocolSpecificRTMP) protocolSpecific() {}

// ProtocolSpecificSRT is the SRT runtime detail for a Stream.
type ProtocolSpecificSRT struct {
	PacketsReceived  int64  `json:"packets_received"`
	PacketsLost      int64  `json:"packets_lost"`
	PacketsDropped   int64  `json:"packets_dropped"`
	Retransmits      int64  `json:"retransmits"`
	EncryptionState  string `json:"encryption_state"`
}

func (*ProtocolSpecificSRT) protocolSpecific() {}

// ProtocolSpecificWebRTC is the WebRTC runtime detail for a Stream.
//
// Local and remote candidate descriptors that include IPs are PII;
// handlers redact at the API boundary.
type ProtocolSpecificWebRTC struct {
	PeerConnectionState string   `json:"peer_connection_state"`
	ICEState            string   `json:"ice_state"`
	LocalCandidates     []string `json:"local_candidates,omitempty"`
	RemoteCandidates    []string `json:"remote_candidates,omitempty"`
}

func (*ProtocolSpecificWebRTC) protocolSpecific() {}

// ProtocolSpecificHLS is the HLS runtime detail for a Stream.
type ProtocolSpecificHLS struct {
	OutboundBytes int64      `json:"outbound_bytes"`
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
}

func (*ProtocolSpecificHLS) protocolSpecific() {}
