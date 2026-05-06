// Package onvif implements the recorder's ONVIF subsystem: WS-Discovery,
// GetDeviceInformation, and PullPoint event subscriptions.
//
// Hand-rolled SOAP-over-XML using only the Go stdlib (encoding/xml +
// net/http + net) per AGENTS.md §8 ("prefer the standard library and
// existing dependencies"). A third-party ONVIF library would have
// brought a large surface area of code we don't need — the recorder
// only consumes three discrete operations from the ONVIF spec:
//
//  1. WS-Discovery Probe / ProbeMatch over UDP multicast 239.255.255.250:3702
//     (DPWS, the lightweight discovery sub-protocol used by ONVIF Profile S/T)
//  2. tds:GetDeviceInformation against /onvif/device_service over HTTPS or HTTP
//  3. tev:CreatePullPointSubscription / tev:PullMessages / tev:Renew /
//     tev:Unsubscribe against the camera's event service URL
//
// Each is a fixed SOAP envelope with a known response shape. The XML
// layer is small enough that hand-rolled is more maintainable than a
// dependency.
//
// References:
//   - ONVIF Core Specification (https://www.onvif.org/specs/core/ONVIF-Core-Specification.pdf)
//   - WS-Discovery (https://docs.oasis-open.org/ws-dd/ns/discovery/2009/01)
//   - WS-BaseNotification (https://docs.oasis-open.org/wsn/wsn-ws_base_notification-1.3-spec-os.pdf)
//
// Package layout:
//   - onvif.go         — package overview + shared types + SOAP envelope helpers
//   - discovery.go     — WS-Discovery probe + ProbeMatch parsing
//   - device_info.go   — GetDeviceInformation SOAP request/response
//   - pullpoint.go     — PullPoint subscription lifecycle (create / pull / renew / unsubscribe)
//   - event_topics.go  — ONVIF topic → canonical Event.kind mapping
//   - manager.go       — background subscription manager (one goroutine per active subscription)
//   - testfixtures.go  — XML fixtures for tests, kept package-internal so tests don't repeat them
package onvif

import (
	"crypto/sha1" //nolint:gosec // SHA-1 is required by ONVIF WS-Security UsernameToken digest
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// SOAP / WS namespaces used across the ONVIF surface.
const (
	NSEnvelope    = "http://www.w3.org/2003/05/soap-envelope"
	NSAddressing  = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	NSDiscovery   = "http://schemas.xmlsoap.org/ws/2005/04/discovery"
	NSDevice      = "http://www.onvif.org/ver10/device/wsdl"
	NSNetwork     = "http://www.onvif.org/ver10/network/wsdl"
	NSEvents      = "http://www.onvif.org/ver10/events/wsdl"
	NSWSNTopic    = "http://docs.oasis-open.org/wsn/t-1"
	NSWSNotify    = "http://docs.oasis-open.org/wsn/b-2"
	NSWSSecExt    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	NSWSSecUtil   = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
	NSPasswordTyp = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	NSNonceEnc    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary"
)

// SOAP action URIs.
const (
	ActionProbe                     = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe"
	ActionGetDeviceInformation      = "http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"
	ActionCreatePullPointSubscript  = "http://www.onvif.org/ver10/events/wsdl/EventPortType/CreatePullPointSubscriptionRequest"
	ActionPullMessages              = "http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest"
	ActionRenew                     = "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/RenewRequest"
	ActionUnsubscribe               = "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/UnsubscribeRequest"
)

// usernameTokenHeader builds a WS-Security UsernameToken header per the
// OASIS UsernameToken Profile 1.0. The PasswordDigest is
// Base64(SHA1(nonce + created + password)) where nonce is raw bytes and
// created is an ISO-8601 UTC timestamp string.
//
// nonceB64 is the Base64-encoded form of the nonce (16 raw bytes). created
// is the timestamp as it'll appear on the wire. password is the camera
// account's plaintext password.
//
// Returns the marshaled <Security> XML fragment ready to drop inside
// <Header>. Returns "" if username is empty (anonymous request).
func usernameTokenHeader(username, password, nonceB64, created string) string {
	if username == "" {
		return ""
	}
	nonceRaw, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		nonceRaw = []byte(nonceB64)
	}
	h := sha1.New() //nolint:gosec
	h.Write(nonceRaw)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	var sb strings.Builder
	sb.WriteString(`<wsse:Security xmlns:wsse="`)
	sb.WriteString(NSWSSecExt)
	sb.WriteString(`" xmlns:wsu="`)
	sb.WriteString(NSWSSecUtil)
	sb.WriteString(`"><wsse:UsernameToken><wsse:Username>`)
	sb.WriteString(xmlEscape(username))
	sb.WriteString(`</wsse:Username><wsse:Password Type="`)
	sb.WriteString(NSPasswordTyp)
	sb.WriteString(`">`)
	sb.WriteString(digest)
	sb.WriteString(`</wsse:Password><wsse:Nonce EncodingType="`)
	sb.WriteString(NSNonceEnc)
	sb.WriteString(`">`)
	sb.WriteString(nonceB64)
	sb.WriteString(`</wsse:Nonce><wsu:Created>`)
	sb.WriteString(created)
	sb.WriteString(`</wsu:Created></wsse:UsernameToken></wsse:Security>`)
	return sb.String()
}

// xmlEscape escapes the small set of characters that breaks an XML body
// when interpolated as a string. Used by the hand-built envelope
// builders below; full encoder/Marshal isn't needed because the SOAP
// envelopes are static templates with one or two operator-supplied
// fields.
func xmlEscape(s string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// xml.EscapeText doesn't actually return errors for valid input;
		// fall back to a coarse manual escape so tests / unusual inputs
		// don't panic.
		return strings.NewReplacer("<", "&lt;", ">", "&gt;", "&", "&amp;",
			`"`, "&quot;", "'", "&apos;").Replace(s)
	}
	return b.String()
}

// SOAPFault is a minimal SOAP 1.2 fault parsed from a non-200 (or
// 200-with-fault-body) response. Used when the camera rejects a request
// — operator UIs surface fault.Reason for diagnosis.
type SOAPFault struct {
	Code   string
	Reason string
}

// Error implements the error interface so callers can return a SOAPFault
// directly.
func (f *SOAPFault) Error() string {
	if f.Reason != "" {
		return fmt.Sprintf("SOAP fault: %s (%s)", f.Reason, f.Code)
	}
	return fmt.Sprintf("SOAP fault: %s", f.Code)
}

// parseSOAPFault tries to extract a fault from a SOAP response body.
// Returns nil if the body isn't a fault. SOAP 1.2 wraps fault data
// inside <Envelope><Body><Fault><Code><Value>...</Value></Code><Reason><Text>...</Text></Reason></Fault></Body></Envelope>.
func parseSOAPFault(body []byte) *SOAPFault {
	type soapFault struct {
		XMLName xml.Name
		Code    struct {
			Value string `xml:"Value"`
		} `xml:"Code"`
		Reason struct {
			Text string `xml:"Text"`
		} `xml:"Reason"`
	}
	type soapBody struct {
		XMLName xml.Name
		Fault   *soapFault `xml:"Fault"`
	}
	type soapEnvelope struct {
		XMLName xml.Name
		Body    soapBody `xml:"Body"`
	}
	var env soapEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil
	}
	if env.Body.Fault == nil {
		return nil
	}
	return &SOAPFault{
		Code:   env.Body.Fault.Code.Value,
		Reason: env.Body.Fault.Reason.Text,
	}
}

// utcTimestamp formats t as ISO-8601 UTC for WS-Security Created /
// WS-Discovery wsa:MessageID generation. Stable across platforms.
func utcTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
