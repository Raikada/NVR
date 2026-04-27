package defs

import (
	"encoding/json"
	"fmt"
)

// UnmarshalSourceConfig decodes a JSON payload into a concrete
// SourceConfig variant chosen by sourceType. Empty / null payloads
// resolve to nil. Added in Phase 2 of ADR 0009 to make Camera
// round-trippable through JSON, since the Camera.SourceConfig
// interface field can't be auto-populated by encoding/json.
func UnmarshalSourceConfig(sourceType CameraSourceType, raw json.RawMessage) (SourceConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var sc SourceConfig
	switch sourceType {
	case CameraSourceTypeRTSP, CameraSourceTypeRTSPS:
		sc = &SourceConfigRTSP{}
	case CameraSourceTypeRTMP, CameraSourceTypeRTMPS:
		sc = &SourceConfigRTMP{}
	case CameraSourceTypeSRT:
		sc = &SourceConfigSRT{}
	case CameraSourceTypeWHEP:
		sc = &SourceConfigWHEP{}
	case CameraSourceTypeRedirect:
		sc = &SourceConfigRedirect{}
	case CameraSourceTypeFile:
		sc = &SourceConfigFile{}
	case CameraSourceTypePublish:
		sc = &SourceConfigPublish{}
	case CameraSourceTypeRPiCamera:
		sc = &SourceConfigRPiCamera{}
	case CameraSourceTypeRTP:
		sc = &SourceConfigRTP{}
	case CameraSourceTypeHLS:
		sc = &SourceConfigHLS{}
	default:
		return nil, fmt.Errorf("unknown source_type %q", sourceType)
	}
	if err := json.Unmarshal(raw, sc); err != nil {
		return nil, fmt.Errorf("decoding source_config for source_type=%s: %w", sourceType, err)
	}
	return sc, nil
}

// UnmarshalProtocolSpecific decodes a JSON payload into a concrete
// ProtocolSpecific variant chosen by protocol. Empty / null payloads
// resolve to nil. Added in Phase 2 of ADR 0009 to round-trip Stream.
func UnmarshalProtocolSpecific(protocol StreamProtocol, raw json.RawMessage) (ProtocolSpecific, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var ps ProtocolSpecific
	switch protocol {
	case StreamProtocolRTSP, StreamProtocolRTSPS:
		ps = &ProtocolSpecificRTSP{}
	case StreamProtocolRTMP, StreamProtocolRTMPS:
		ps = &ProtocolSpecificRTMP{}
	case StreamProtocolSRT:
		ps = &ProtocolSpecificSRT{}
	case StreamProtocolWebRTC:
		ps = &ProtocolSpecificWebRTC{}
	case StreamProtocolHLS:
		ps = &ProtocolSpecificHLS{}
	default:
		return nil, fmt.Errorf("unknown protocol %q", protocol)
	}
	if err := json.Unmarshal(raw, ps); err != nil {
		return nil, fmt.Errorf("decoding protocol_specific for protocol=%s: %w", protocol, err)
	}
	return ps, nil
}

// cameraJSON mirrors Camera but with a json.RawMessage SourceConfig so
// the discriminated-union dispatch can run after the surrounding fields
// are decoded.
type cameraJSON struct {
	*cameraAlias
	SourceConfig json.RawMessage `json:"source_config,omitempty"`
}

type cameraAlias Camera

// UnmarshalJSON implements json.Unmarshaler for Camera, dispatching the
// SourceConfig discriminated union by SourceType.
func (c *Camera) UnmarshalJSON(data []byte) error {
	aux := cameraJSON{cameraAlias: (*cameraAlias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	sc, err := UnmarshalSourceConfig(c.SourceType, aux.SourceConfig)
	if err != nil {
		return err
	}
	c.SourceConfig = sc
	return nil
}

type streamJSON struct {
	*streamAlias
	ProtocolSpecific json.RawMessage `json:"protocol_specific,omitempty"`
}

type streamAlias Stream

// UnmarshalJSON implements json.Unmarshaler for Stream, dispatching the
// ProtocolSpecific discriminated union by Protocol.
func (s *Stream) UnmarshalJSON(data []byte) error {
	aux := streamJSON{streamAlias: (*streamAlias)(s)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	ps, err := UnmarshalProtocolSpecific(s.Protocol, aux.ProtocolSpecific)
	if err != nil {
		return err
	}
	s.ProtocolSpecific = ps
	return nil
}
