// Package onvif: Media service client (SP2). GetCapabilities lives on
// DeviceClient (it's a device-service call); GetProfiles / GetStreamUri /
// GetSnapshotUri live on MediaClient pointed at the media-service XAddr
// that GetCapabilities returned.
package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Media-service namespace + SOAP actions (SP2 additions).
const (
	NSMedia = "http://www.onvif.org/ver10/media/wsdl"

	ActionGetCapabilities = "http://www.onvif.org/ver10/device/wsdl/GetCapabilities"
	ActionGetProfiles     = "http://www.onvif.org/ver10/media/wsdl/GetProfiles"
	ActionGetStreamURI    = "http://www.onvif.org/ver10/media/wsdl/GetStreamUri"
	ActionGetSnapshotURI  = "http://www.onvif.org/ver10/media/wsdl/GetSnapshotUri"
)

// Capabilities is the tds:GetCapabilities subset SP2 consumes: service
// XAddrs plus feature booleans derived from section presence.
type Capabilities struct {
	MediaXAddr  string `json:"media_xaddr"`
	EventsXAddr string `json:"events_xaddr"`
	HasPTZ      bool   `json:"has_ptz"`
	HasImaging  bool   `json:"has_imaging"`
	HasIO       bool   `json:"has_io"`
}

// MediaProfile is one ONVIF media profile: the token to request streams
// with, plus what the profile carries.
type MediaProfile struct {
	Token      string `json:"token"`
	Name       string `json:"name"`
	VideoCodec string `json:"video_codec"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	HasAudio   bool   `json:"has_audio"`
}

// MediaClient sends ONVIF media-service requests. Same transport and
// WS-Security semantics as DeviceClient, pointed at the media XAddr.
type MediaClient struct {
	XAddr    string
	Username string
	Password string

	HTTPClient *http.Client
	Now        func() time.Time
	NewNonce   func() string
}

// GetCapabilities issues tds:GetCapabilities (Category All) and parses
// the service XAddrs + feature booleans.
func (c *DeviceClient) GetCapabilities(ctx context.Context) (*Capabilities, error) {
	if c.XAddr == "" {
		return nil, fmt.Errorf("xaddr is required")
	}
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:tds="` + NSDevice + `">` +
		`<s:Header>` + c.buildAuthHeader() + `</s:Header>` +
		`<s:Body><tds:GetCapabilities><tds:Category>All</tds:Category></tds:GetCapabilities></s:Body>` +
		`</s:Envelope>`
	respBody, err := c.do(ctx, ActionGetCapabilities, []byte(envelope))
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Capabilities struct {
					Device struct {
						XAddr string `xml:"XAddr"`
						IO    *struct {
							InputConnectors int `xml:"InputConnectors"`
							RelayOutputs    int `xml:"RelayOutputs"`
						} `xml:"IO"`
					} `xml:"Device"`
					Events *struct {
						XAddr string `xml:"XAddr"`
					} `xml:"Events"`
					Imaging *struct {
						XAddr string `xml:"XAddr"`
					} `xml:"Imaging"`
					Media *struct {
						XAddr string `xml:"XAddr"`
					} `xml:"Media"`
					PTZ *struct {
						XAddr string `xml:"XAddr"`
					} `xml:"PTZ"`
				} `xml:"Capabilities"`
			} `xml:"GetCapabilitiesResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return nil, f
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}
	caps := env.Body.Response.Capabilities
	out := &Capabilities{
		HasPTZ:     caps.PTZ != nil && caps.PTZ.XAddr != "",
		HasImaging: caps.Imaging != nil && caps.Imaging.XAddr != "",
		HasIO: caps.Device.IO != nil &&
			(caps.Device.IO.InputConnectors > 0 || caps.Device.IO.RelayOutputs > 0),
	}
	if caps.Media != nil {
		out.MediaXAddr = strings.TrimSpace(caps.Media.XAddr)
	}
	if caps.Events != nil {
		out.EventsXAddr = strings.TrimSpace(caps.Events.XAddr)
	}
	return out, nil
}

// GetProfiles issues trt:GetProfiles and parses the media profiles.
func (c *MediaClient) GetProfiles(ctx context.Context) ([]MediaProfile, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:trt="` + NSMedia + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><trt:GetProfiles/></s:Body>` +
		`</s:Envelope>`
	respBody, err := c.do(ctx, ActionGetProfiles, []byte(envelope))
	if err != nil {
		return nil, err
	}

	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Profiles []struct {
					Token string `xml:"token,attr"`
					Name  string `xml:"Name"`
					Video *struct {
						Encoding   string `xml:"Encoding"`
						Resolution struct {
							Width  int `xml:"Width"`
							Height int `xml:"Height"`
						} `xml:"Resolution"`
					} `xml:"VideoEncoderConfiguration"`
					Audio *struct {
						Encoding string `xml:"Encoding"`
					} `xml:"AudioEncoderConfiguration"`
				} `xml:"Profiles"`
			} `xml:"GetProfilesResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return nil, f
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := make([]MediaProfile, 0, len(env.Body.Response.Profiles))
	for _, p := range env.Body.Response.Profiles {
		mp := MediaProfile{
			Token:    p.Token,
			Name:     strings.TrimSpace(p.Name),
			HasAudio: p.Audio != nil,
		}
		if p.Video != nil {
			mp.VideoCodec = strings.TrimSpace(p.Video.Encoding)
			mp.Width = p.Video.Resolution.Width
			mp.Height = p.Video.Resolution.Height
		}
		out = append(out, mp)
	}
	return out, nil
}

// GetStreamURI issues trt:GetStreamUri for the profile and returns the
// RTSP URL with any camera-embedded userinfo stripped — credentials
// live in the vault, never in stored source URLs.
func (c *MediaClient) GetStreamURI(ctx context.Context, profileToken string) (string, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:trt="` + NSMedia + `"` +
		` xmlns:tt="http://www.onvif.org/ver10/schema">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><trt:GetStreamUri>` +
		`<trt:StreamSetup>` +
		`<tt:Stream>RTP-Unicast</tt:Stream>` +
		`<tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport>` +
		`</trt:StreamSetup>` +
		`<trt:ProfileToken>` + xmlEscape(profileToken) + `</trt:ProfileToken>` +
		`</trt:GetStreamUri></s:Body>` +
		`</s:Envelope>`
	uri, err := c.mediaURIRequest(ctx, ActionGetStreamURI, envelope, "GetStreamUriResponse")
	if err != nil {
		return "", err
	}
	return stripUserinfo(uri), nil
}

// GetSnapshotURI issues trt:GetSnapshotUri for the profile.
func (c *MediaClient) GetSnapshotURI(ctx context.Context, profileToken string) (string, error) {
	envelope := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `" xmlns:trt="` + NSMedia + `">` +
		`<s:Header>` + c.authHeader() + `</s:Header>` +
		`<s:Body><trt:GetSnapshotUri>` +
		`<trt:ProfileToken>` + xmlEscape(profileToken) + `</trt:ProfileToken>` +
		`</trt:GetSnapshotUri></s:Body>` +
		`</s:Envelope>`
	uri, err := c.mediaURIRequest(ctx, ActionGetSnapshotURI, envelope, "GetSnapshotUriResponse")
	if err != nil {
		return "", err
	}
	return stripUserinfo(uri), nil
}

// mediaURIRequest handles the shared MediaUri/Uri response shape of
// GetStreamUri and GetSnapshotUri.
func (c *MediaClient) mediaURIRequest(ctx context.Context, action, envelope, responseElement string) (string, error) {
	respBody, err := c.do(ctx, action, []byte(envelope))
	if err != nil {
		return "", err
	}
	var env struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Inner struct {
				XMLName  xml.Name
				MediaURI struct {
					URI string `xml:"Uri"`
				} `xml:"MediaUri"`
			} `xml:",any"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(respBody, &env); err != nil {
		if f := parseSOAPFault(respBody); f != nil {
			return "", f
		}
		return "", fmt.Errorf("decode response: %w", err)
	}
	if env.Body.Inner.XMLName.Local != responseElement {
		if f := parseSOAPFault(respBody); f != nil {
			return "", f
		}
		return "", fmt.Errorf("unexpected response element %q (want %q)",
			env.Body.Inner.XMLName.Local, responseElement)
	}
	uri := strings.TrimSpace(env.Body.Inner.MediaURI.URI)
	if uri == "" {
		return "", fmt.Errorf("%s carried no Uri", responseElement)
	}
	return uri, nil
}

// authHeader mirrors DeviceClient.buildAuthHeader for MediaClient.
func (c *MediaClient) authHeader() string {
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

// do mirrors DeviceClient.do (same transport rules) for MediaClient.
func (c *MediaClient) do(ctx context.Context, action string, envelope []byte) ([]byte, error) {
	dc := &DeviceClient{XAddr: c.XAddr, HTTPClient: c.HTTPClient}
	return dc.do(ctx, action, envelope)
}

// stripUserinfo removes user:password@ from a URL, tolerating inputs
// that fail full URL parsing by returning them unchanged.
func stripUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}
