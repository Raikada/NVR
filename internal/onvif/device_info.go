// GetDeviceInformation SOAP request. Sends a tds:GetDeviceInformation
// request to the camera's /onvif/device_service endpoint and parses
// the response for Manufacturer / Model / FirmwareVersion / SerialNumber
// / HardwareId.
//
// Auth: ONVIF cameras either accept anonymous requests for
// GetDeviceInformation (most consumer brands) or require a WS-Security
// UsernameToken (most enterprise brands). The caller supplies username +
// password if known; otherwise we send anonymous and the camera surfaces
// the auth requirement as a SOAP fault — the operator UI then re-prompts.

package onvif

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultDeviceInfoTimeout is the HTTP deadline for GetDeviceInformation.
// 5 seconds covers slow embedded TLS handshakes without making the
// operator wait too long.
const DefaultDeviceInfoTimeout = 5 * time.Second

// DeviceInformation is the structured response from
// tds:GetDeviceInformation. Maps directly to the SOAP response body.
type DeviceInformation struct {
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	FirmwareVersion string `json:"firmware_version"`
	SerialNumber    string `json:"serial_number"`
	HardwareID      string `json:"hardware_id"`
}

// DeviceClient sends ONVIF requests against a single camera's device
// service URL. Reusable across requests; thread-safe for concurrent use.
type DeviceClient struct {
	// XAddr is the camera's device service URL, typically
	// http://<camera>/onvif/device_service. Discovered via WS-Discovery.
	XAddr string

	// Username + Password are the camera-account credentials. Both empty
	// = anonymous request.
	Username string
	Password string

	// HTTPClient is the underlying HTTP transport. nil = a stock client
	// with TLS verification disabled (cameras commonly ship with
	// self-signed certs; trust is established at LAN-physical layer).
	HTTPClient *http.Client

	// Now is the time source for WS-Security Created timestamps. nil =
	// time.Now().
	Now func() time.Time

	// NewNonce is the source of WS-Security UsernameToken nonces. nil =
	// 16 bytes from crypto/rand encoded as Base64.
	NewNonce func() string
}

// GetDeviceInformation issues a tds:GetDeviceInformation SOAP request
// and parses the response.
func (c *DeviceClient) GetDeviceInformation(ctx context.Context) (DeviceInformation, error) {
	if c.XAddr == "" {
		return DeviceInformation{}, fmt.Errorf("xaddr is required")
	}

	envelope := c.buildGetDeviceInformationEnvelope()
	respBody, err := c.do(ctx, ActionGetDeviceInformation, envelope)
	if err != nil {
		return DeviceInformation{}, err
	}

	var env getDeviceInfoEnvelope
	if err := xml.Unmarshal(respBody, &env); err != nil {
		// Try to surface a SOAP fault first.
		if f := parseSOAPFault(respBody); f != nil {
			return DeviceInformation{}, f
		}
		return DeviceInformation{}, fmt.Errorf("decode response: %w", err)
	}
	if env.Body.Fault != nil {
		return DeviceInformation{}, &SOAPFault{
			Code:   env.Body.Fault.Code.Value,
			Reason: env.Body.Fault.Reason.Text,
		}
	}
	r := env.Body.Response
	return DeviceInformation{
		Manufacturer:    strings.TrimSpace(r.Manufacturer),
		Model:           strings.TrimSpace(r.Model),
		FirmwareVersion: strings.TrimSpace(r.FirmwareVersion),
		SerialNumber:    strings.TrimSpace(r.SerialNumber),
		HardwareID:      strings.TrimSpace(r.HardwareID),
	}, nil
}

func (c *DeviceClient) buildGetDeviceInformationEnvelope() []byte {
	header := c.buildAuthHeader()
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:tds="` + NSDevice + `">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body><tds:GetDeviceInformation/></s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

// buildAuthHeader returns the Security header fragment when
// credentials are supplied, else "".
func (c *DeviceClient) buildAuthHeader() string {
	if c.Username == "" {
		return ""
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	created := utcTimestamp(now())

	nonce := c.NewNonce
	if nonce == nil {
		nonce = randomNonce
	}
	return usernameTokenHeader(c.Username, c.Password, nonce(), created)
}

// randomNonce returns 16 random bytes encoded as Base64.
func randomNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString(b[:])
}

// do POSTs the SOAP envelope and returns the raw body.
func (c *DeviceClient) do(ctx context.Context, action string, envelope []byte) ([]byte, error) {
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: DefaultDeviceInfoTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.XAddr, bytes.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Some cameras require the SOAP 1.2 Content-Type with application/soap+xml;
	// SOAP-Action goes in the header for SOAP 1.1 compatibility, and as a
	// charset/action parameter for SOAP 1.2.
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="`+action+`"`)
	req.Header.Set("SOAPAction", action)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return body, fmt.Errorf("authentication required: HTTP %d", resp.StatusCode)
	}
	// Some cameras return SOAP faults with HTTP 500; surface the body
	// to parseSOAPFault rather than treating as a generic 5xx.
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusInternalServerError {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// getDeviceInfoEnvelope is the parsed shape of a successful
// GetDeviceInformationResponse.
type getDeviceInfoEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Fault *struct {
			Code struct {
				Value string `xml:"Value"`
			} `xml:"Code"`
			Reason struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
		} `xml:"Fault"`
		Response struct {
			Manufacturer    string `xml:"Manufacturer"`
			Model           string `xml:"Model"`
			FirmwareVersion string `xml:"FirmwareVersion"`
			SerialNumber    string `xml:"SerialNumber"`
			HardwareID      string `xml:"HardwareId"`
		} `xml:"GetDeviceInformationResponse"`
	} `xml:"Body"`
}
