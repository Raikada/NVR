package amcrestchannel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBlockVideoMotionStart(t *testing.T) {
	ev := parseBlock([]string{
		"Content-Type: text/plain",
		"Content-Length:39",
		"Code=VideoMotion;action=Start;index=0",
	})
	require.NotNil(t, ev)
	require.Equal(t, "VideoMotion", ev.Code)
	require.Equal(t, "Start", ev.Action)
	require.Equal(t, 0, ev.Index)
}

func TestParseBlockSmartMotionHumanWithData(t *testing.T) {
	ev := parseBlock([]string{
		"Content-Type: text/plain",
		"Content-Length:120",
		`Code=SmartMotionHuman;action=Start;index=0;data={`,
		`   "RegionName" : [ "Region1" ],`,
		`   "WindowId" : [ 0 ]`,
		`}`,
	})
	require.NotNil(t, ev)
	require.Equal(t, "SmartMotionHuman", ev.Code)
	require.Equal(t, "Start", ev.Action)
	require.Contains(t, ev.DataJSON, "RegionName")
}

func TestParseBlockHeartbeat(t *testing.T) {
	ev := parseBlock([]string{
		"Content-Type: text/plain",
		"Content-Length:9",
		"Heartbeat",
	})
	require.NotNil(t, ev)
	require.Equal(t, "Heartbeat", ev.Code)
}

func TestParseBlockGarbageReturnsNil(t *testing.T) {
	require.Nil(t, parseBlock([]string{"Content-Type: text/plain", "???"}))
	require.Nil(t, parseBlock(nil))
}

func TestMapEvent(t *testing.T) {
	cases := []struct {
		code, action string
		wantType     string
		wantEmit     bool
	}{
		{"VideoMotion", "Start", "motion", true},
		{"VideoMotion", "Stop", "", false},
		{"SmartMotionHuman", "Start", "person", true},
		{"SmartMotionVehicle", "Start", "vehicle", true},
		{"CrossLineDetection", "Start", "line_cross", true},
		{"CrossRegionDetection", "Start", "motion", true},
		{"VideoBlind", "Start", "tamper", true},
		{"AudioMutation", "Start", "audio_alarm", true},
		{"AlarmLocal", "Start", "io_in", true},
		{"CallNoAnswered", "Start", "doorbell", true},
		{"CallNoAnswered", "Pulse", "doorbell", true},
		{"_DoTalkAction_", "Invite", "doorbell", true},
		{"Invite", "Start", "doorbell", true},
		{"Heartbeat", "", "", false},
		{"StorageFailure", "Start", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.code+"/"+tc.action, func(t *testing.T) {
			ne, emit := mapEvent(&cgiEvent{Code: tc.code, Action: tc.action})
			require.Equal(t, tc.wantEmit, emit)
			if emit {
				require.Equal(t, tc.wantType, ne.TypeID)
			}
		})
	}
}
