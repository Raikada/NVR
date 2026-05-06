// ONVIF event topic → canonical Event.kind mapping.
//
// The recorder translates ONVIF NotificationMessage.Topic strings to
// the canonical Event.kind vocabulary defined in domain-model.md.
// Mappings cover the common ONVIF Profile S/T topics; unmapped topics
// fall through to a generic camera.onvif_event with the original topic
// in attributes so consumers can still observe them.
//
// The table is intentionally conservative. Adding new mappings is an
// additive change; renaming an existing canonical kind is a domain-
// model.md amendment + ADR. Sources:
//
//   - ONVIF Core Specification §10 (Event Service)
//   - ONVIF DeviceIO Specification (tns1:Device topics)
//   - Real-camera observations from Hikvision, Axis, Dahua, Bosch
//     for the per-vendor variations that don't appear in the core
//     spec.

package onvif

import (
	"strings"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// EventMapping is the canonical translation of a single ONVIF topic.
// Severity defaults to info; mapping entries override for topics that
// represent operational incidents (tamper, communication loss).
type EventMapping struct {
	Kind     string
	Severity defs.EventSeverity
	Message  string
}

// CanonicalEventKindForTopic looks up the mapping for an ONVIF topic.
// Public alias for canonicalEventKind so internal/api can call without
// re-implementing the table.
func CanonicalEventKindForTopic(topic string) (EventMapping, bool) {
	return canonicalEventKind(topic)
}

// canonicalEventKind looks up the mapping for an ONVIF topic.
//
// Topic prefixes vary by vendor and ONVIF profile version; we strip the
// "tns1:" / "tnsaxis:" / etc. prefix and match against a single
// vocabulary. ONVIF cameras universally use the tns1: namespace for
// the core topic tree, so this matches the wire format directly for
// all standard topics.
//
// Unknown topic returns ("", default mapping); callers fall through to
// a generic camera.onvif_event Event.
func canonicalEventKind(topic string) (EventMapping, bool) {
	stripped := stripTopicPrefix(topic)
	if mapping, ok := canonicalTopicTable[stripped]; ok {
		return mapping, true
	}
	// Vendor-extension topics (tnsaxis:, tnshk:, tnsdh:) don't appear in
	// the core table; we still mine them for substring matches against
	// the well-known event categories.
	if strings.Contains(strings.ToLower(stripped), "motion") {
		return EventMapping{
			Kind:     "camera.motion_detected",
			Severity: defs.EventSeverityInfo,
			Message:  "motion detected (vendor extension)",
		}, true
	}
	if strings.Contains(strings.ToLower(stripped), "tamper") {
		return EventMapping{
			Kind:     "camera.tamper_detected",
			Severity: defs.EventSeverityWarning,
			Message:  "tamper detected (vendor extension)",
		}, true
	}
	return EventMapping{}, false
}

// stripTopicPrefix removes the namespace prefix (e.g., "tns1:") from a
// topic. Cameras using non-tns1: prefixes are common.
func stripTopicPrefix(topic string) string {
	if idx := strings.Index(topic, ":"); idx >= 0 {
		return topic[idx+1:]
	}
	return topic
}

// canonicalTopicTable maps standard ONVIF topics to canonical Event
// kinds. Topic strings are post-stripPrefix form, i.e., what comes
// after "tns1:".
var canonicalTopicTable = map[string]EventMapping{
	// Motion / video analytics
	"VideoSource/MotionAlarm": {
		Kind:     "camera.motion_detected",
		Severity: defs.EventSeverityInfo,
		Message:  "motion detected",
	},
	"RuleEngine/CellMotionDetector/Motion": {
		Kind:     "camera.motion_detected",
		Severity: defs.EventSeverityInfo,
		Message:  "cell motion detected",
	},
	"RuleEngine/MotionRegionDetector/Motion": {
		Kind:     "camera.motion_detected",
		Severity: defs.EventSeverityInfo,
		Message:  "region motion detected",
	},
	"RuleEngine/FieldDetector/ObjectsInside": {
		Kind:     "camera.field_detection",
		Severity: defs.EventSeverityInfo,
		Message:  "objects in field detected",
	},
	"RuleEngine/LineDetector/Crossed": {
		Kind:     "camera.line_crossing",
		Severity: defs.EventSeverityInfo,
		Message:  "line crossing detected",
	},

	// Tampering
	"VideoSource/ImageTooDark/AnalyticsService": {
		Kind:     "camera.tamper_detected",
		Severity: defs.EventSeverityWarning,
		Message:  "image too dark — possible tamper",
	},
	"VideoSource/ImageTooBright/AnalyticsService": {
		Kind:     "camera.tamper_detected",
		Severity: defs.EventSeverityWarning,
		Message:  "image too bright — possible tamper",
	},
	"VideoSource/ImageTooBlurry/AnalyticsService": {
		Kind:     "camera.tamper_detected",
		Severity: defs.EventSeverityWarning,
		Message:  "image too blurry — possible tamper",
	},
	"VideoSource/SignalLoss": {
		Kind:     "camera.signal_loss",
		Severity: defs.EventSeverityWarning,
		Message:  "video signal loss",
	},
	"VideoSource/GlobalSceneChange/AnalyticsService": {
		Kind:     "camera.scene_change",
		Severity: defs.EventSeverityInfo,
		Message:  "global scene change",
	},

	// Audio
	"AudioAnalytics/Audio/DetectedSound": {
		Kind:     "camera.audio_detected",
		Severity: defs.EventSeverityInfo,
		Message:  "audio detected",
	},

	// Device / hardware
	"Device/Trigger/DigitalInput": {
		Kind:     "camera.digital_input",
		Severity: defs.EventSeverityInfo,
		Message:  "digital input triggered",
	},
	"Device/Trigger/Relay": {
		Kind:     "camera.relay_trigger",
		Severity: defs.EventSeverityInfo,
		Message:  "relay triggered",
	},
	"Device/HardwareFailure/StorageFailure": {
		Kind:     "camera.storage_failure",
		Severity: defs.EventSeverityError,
		Message:  "camera storage failure",
	},
	"Device/HardwareFailure/PowerFailure": {
		Kind:     "camera.power_failure",
		Severity: defs.EventSeverityError,
		Message:  "camera power failure",
	},

	// Recording
	"RecordingHistory/Recording/State": {
		Kind:     "camera.recording_state",
		Severity: defs.EventSeverityInfo,
		Message:  "recording state changed",
	},

	// Alarms
	"Monitoring/ProcessorUsage": {
		Kind:     "camera.processor_usage",
		Severity: defs.EventSeverityInfo,
		Message:  "processor usage report",
	},

	// PTZ
	"PTZController/PTZPresets/Reached": {
		Kind:     "camera.ptz_preset_reached",
		Severity: defs.EventSeverityInfo,
		Message:  "PTZ preset reached",
	},
}

// genericONVIFKind is the fallback Event.kind for unmapped topics. The
// raw ONVIF topic lands in attributes so consumers can correlate by
// vendor / topic without a recorder-side mapping change.
const genericONVIFKind = "camera.onvif_event"
