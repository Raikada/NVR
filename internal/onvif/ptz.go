// Package onvif: PTZ service client + device reboot (Phase A B5).
// Same WS-Security transport as DeviceClient/MediaClient, pointed at
// the PTZ service XAddr (usually the device service URL).
package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PTZ namespace + actions.
const (
	NSPTZ = "http://www.onvif.org/ver20/ptz/wsdl"

	ActionPTZContinuousMove = "http://www.onvif.org/ver20/ptz/wsdl/ContinuousMove"
	ActionPTZStop           = "http://www.onvif.org/ver20/ptz/wsdl/Stop"
	ActionPTZGotoHome       = "http://www.onvif.org/ver20/ptz/wsdl/GotoHomePosition"
	ActionPTZGetPresets     = "http://www.onvif.org/ver20/ptz/wsdl/GetPresets"
	ActionPTZGotoPreset     = "http://www.onvif.org/ver20/ptz/wsdl/GotoPreset"
	ActionPTZSetPreset      = "http://www.onvif.org/ver20/ptz/wsdl/SetPreset"
	ActionSystemReboot      = "http://www.onvif.org/ver10/device/wsdl/SystemReboot"
)

// PTZPreset is one named camera-side position.
type PTZPreset struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// PTZClient drives the ONVIF PTZ service for one camera.
type PTZClient struct {
	XAddr    string
	Username string
	Password string

	HTTPClient *http.Client
	Now        func() time.Time
	NewNonce   func() string
}

func (c *PTZClient) authHeader() string {
	if c.Username == "" {
		return ""
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	nonce := c.NewNonce
	if nonce == nil {
		nonce = randomNonce
	}
	return usernameTokenHeader(c.Username, c.Password, nonce(), utcTimestamp(now()))
}

func (c *PTZClient) do(ctx context.Context, action, envelope string) ([]byte, error) {
	dc := &DeviceClient{XAddr: c.XAddr, HTTPClient: c.HTTPClient}
	return dc.do(ctx, action, []byte(envelope))
}

// checkFault surfaces SOAP faults from an otherwise-ignored response.
func checkFault(respBody []byte) error {
	if f := parseSOAPFault(respBody); f != nil {
		return f
	}
	return nil
}

func fmtFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ContinuousMove starts panning/tilting/zooming at the given
// velocities (each in -1..1; zero = no movement on that axis). Motion
// continues until Stop.
func (c *PTZClient) ContinuousMove(ctx context.Context, profileToken string, panX, tiltY, zoom float64) error {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `"` +
		` xmlns:tt="http://www.onvif.org/ver10/schema">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:ContinuousMove>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`<tptz:Velocity>` +
		`<tt:PanTilt x="` + fmtFloat(panX) + `" y="` + fmtFloat(tiltY) + `"/>` +
		`<tt:Zoom x="` + fmtFloat(zoom) + `"/>` +
		`</tptz:Velocity>` +
		`</tptz:ContinuousMove></s:Body>` +
		`</s:Envelope>`
	resp, err := c.do(ctx, ActionPTZContinuousMove, envelope)
	if err != nil {
		return err
	}
	return checkFault(resp)
}

// Stop halts all PTZ motion on the profile.
func (c *PTZClient) Stop(ctx context.Context, profileToken string) error {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:Stop>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`<tptz:PanTilt>true</tptz:PanTilt><tptz:Zoom>true</tptz:Zoom>` +
		`</tptz:Stop></s:Body>` +
		`</s:Envelope>`
	resp, err := c.do(ctx, ActionPTZStop, envelope)
	if err != nil {
		return err
	}
	return checkFault(resp)
}

// GotoHome recalls the camera's home position.
func (c *PTZClient) GotoHome(ctx context.Context, profileToken string) error {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:GotoHomePosition>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`</tptz:GotoHomePosition></s:Body>` +
		`</s:Envelope>`
	resp, err := c.do(ctx, ActionPTZGotoHome, envelope)
	if err != nil {
		return err
	}
	return checkFault(resp)
}

// GetPresets lists the camera-side named positions.
func (c *PTZClient) GetPresets(ctx context.Context, profileToken string) ([]PTZPreset, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:GetPresets>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`</tptz:GetPresets></s:Body>` +
		`</s:Envelope>`
	respBody, err := c.do(ctx, ActionPTZGetPresets, envelope)
	if err != nil {
		return nil, err
	}
	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Presets []struct {
					Token string `xml:"token,attr"`
					Name  string `xml:"Name"`
				} `xml:"Preset"`
			} `xml:"GetPresetsResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return nil, f
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}
	out := make([]PTZPreset, 0, len(env.Body.Response.Presets))
	for _, p := range env.Body.Response.Presets {
		out = append(out, PTZPreset{Token: p.Token, Name: strings.TrimSpace(p.Name)})
	}
	return out, nil
}

// GotoPreset recalls a preset by token.
func (c *PTZClient) GotoPreset(ctx context.Context, profileToken, presetToken string) error {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:GotoPreset>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`<tptz:PresetToken>` + xmlEscape(presetToken) + `</tptz:PresetToken>` +
		`</tptz:GotoPreset></s:Body>` +
		`</s:Envelope>`
	resp, err := c.do(ctx, ActionPTZGotoPreset, envelope)
	if err != nil {
		return err
	}
	return checkFault(resp)
}

// SetPreset saves the current position under a name; returns the
// camera-assigned preset token.
func (c *PTZClient) SetPreset(ctx context.Context, profileToken, name string) (string, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tptz="` + NSPTZ + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><tptz:SetPreset>` +
		`<tptz:ProfileToken>` + xmlEscape(profileToken) + `</tptz:ProfileToken>` +
		`<tptz:PresetName>` + xmlEscape(name) + `</tptz:PresetName>` +
		`</tptz:SetPreset></s:Body>` +
		`</s:Envelope>`
	respBody, err := c.do(ctx, ActionPTZSetPreset, envelope)
	if err != nil {
		return "", err
	}
	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				PresetToken string `xml:"PresetToken"`
			} `xml:"SetPresetResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return "", f
		}
		return "", fmt.Errorf("decode response: %w", err)
	}
	return env.Body.Response.PresetToken, nil
}

// SystemReboot asks the camera to reboot; returns the camera's
// human-readable message.
func (c *DeviceClient) SystemReboot(ctx context.Context) (string, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tds="` + NSDevice + `">` +
		`<s:Header>` + c.buildAuthHeader() + `</s:Header>` +
		`<s:Body><tds:SystemReboot/></s:Body>` +
		`</s:Envelope>`
	respBody, err := c.do(ctx, ActionSystemReboot, []byte(envelope))
	if err != nil {
		return "", err
	}
	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Message string `xml:"Message"`
			} `xml:"SystemRebootResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return "", f
		}
		return "", fmt.Errorf("decode response: %w", err)
	}
	return strings.TrimSpace(env.Body.Response.Message), nil
}
