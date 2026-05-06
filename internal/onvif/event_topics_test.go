package onvif

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalEventKind_KnownTopics(t *testing.T) {
	cases := []struct {
		topic    string
		wantKind string
	}{
		{"tns1:VideoSource/MotionAlarm", "camera.motion_detected"},
		{"tns1:RuleEngine/CellMotionDetector/Motion", "camera.motion_detected"},
		{"tns1:VideoSource/ImageTooDark/AnalyticsService", "camera.tamper_detected"},
		{"tns1:VideoSource/SignalLoss", "camera.signal_loss"},
		{"tns1:Device/Trigger/DigitalInput", "camera.digital_input"},
		{"tns1:Device/HardwareFailure/StorageFailure", "camera.storage_failure"},
		{"tns1:RuleEngine/LineDetector/Crossed", "camera.line_crossing"},
		{"tns1:PTZController/PTZPresets/Reached", "camera.ptz_preset_reached"},
	}
	for _, c := range cases {
		mapping, ok := canonicalEventKind(c.topic)
		require.True(t, ok, "topic %s should map", c.topic)
		require.Equal(t, c.wantKind, mapping.Kind)
	}
}

func TestCanonicalEventKind_VendorExtension_Motion(t *testing.T) {
	// tnsaxis:CameraApplicationPlatform/Motion would be a vendor topic;
	// our heuristic falls through to camera.motion_detected.
	mapping, ok := canonicalEventKind("tnsaxis:CameraApplicationPlatform/Motion/Detected")
	require.True(t, ok)
	require.Equal(t, "camera.motion_detected", mapping.Kind)
}

func TestCanonicalEventKind_VendorExtension_Tamper(t *testing.T) {
	mapping, ok := canonicalEventKind("tnshk:ipcam/AnalyticsService/Tamper")
	require.True(t, ok)
	require.Equal(t, "camera.tamper_detected", mapping.Kind)
}

func TestCanonicalEventKind_Unknown_FallsThroughToFalse(t *testing.T) {
	_, ok := canonicalEventKind("tns1:Some/Unknown/Topic")
	require.False(t, ok)
}

func TestCanonicalEventKind_Severity_Defaults(t *testing.T) {
	// Motion is info; tamper is warning; storage failure is error.
	m, _ := canonicalEventKind("tns1:VideoSource/MotionAlarm")
	require.Equal(t, "info", string(m.Severity))
	t2, _ := canonicalEventKind("tns1:VideoSource/ImageTooDark/AnalyticsService")
	require.Equal(t, "warning", string(t2.Severity))
	st, _ := canonicalEventKind("tns1:Device/HardwareFailure/StorageFailure")
	require.Equal(t, "error", string(st.Severity))
}

func TestStripTopicPrefix(t *testing.T) {
	require.Equal(t, "VideoSource/Motion", stripTopicPrefix("tns1:VideoSource/Motion"))
	require.Equal(t, "Foo/Bar", stripTopicPrefix("Foo/Bar"))
}
