package onvifchannel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

func TestMapNotification(t *testing.T) {
	cases := []struct {
		name     string
		topic    string
		data     map[string]string
		oper     string
		wantType string
		wantEmit bool
	}{
		{"motion rising", "tns1:RuleEngine/CellMotionDetector/Motion",
			map[string]string{"IsMotion": "true"}, "Changed", "motion", true},
		{"motion falling dropped", "tns1:RuleEngine/CellMotionDetector/Motion",
			map[string]string{"IsMotion": "false"}, "Changed", "", false},
		{"initialized dropped", "tns1:RuleEngine/CellMotionDetector/Motion",
			map[string]string{"IsMotion": "true"}, "Initialized", "", false},
		{"line crossing", "tns1:RuleEngine/LineDetector/Crossed",
			map[string]string{}, "Changed", "line_cross", true},
		{"tamper", "tns1:VideoSource/GlobalSceneChange/ImagingService",
			map[string]string{"State": "true"}, "Changed", "tamper", true},
		{"audio", "tns1:AudioAnalytics/Audio/DetectedSound",
			map[string]string{"State": "true"}, "Changed", "audio_alarm", true},
		{"digital input", "tns1:Device/Trigger/DigitalInput",
			map[string]string{"LogicalState": "true"}, "Changed", "io_in", true},
		{"human classification", "tns1:RuleEngine/MyRuleDetector/PeopleDetect",
			map[string]string{"ObjectClass": "Human", "State": "true"}, "Changed", "person", true},
		{"vehicle classification", "tns1:RuleEngine/MyRuleDetector/VehicleDetect",
			map[string]string{"ObjectClass": "Vehicle", "State": "true"}, "Changed", "vehicle", true},
		{"unknown topic dropped", "tns1:Monitoring/Backup/Last",
			map[string]string{}, "Changed", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ne, emit := mapNotification(onvif.EventNotification{
				Topic:        tc.topic,
				Data:         tc.data,
				PropertyOper: tc.oper,
				UTCTime:      time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC),
			})
			require.Equal(t, tc.wantEmit, emit)
			if emit {
				require.Equal(t, tc.wantType, ne.TypeID)
				require.False(t, ne.OccurredAt.IsZero())
			}
		})
	}
}

func TestRisingEdgeSuppression(t *testing.T) {
	// Repeated State=true for the same topic emits only once until a
	// false resets the edge.
	f := newEdgeFilter()
	ev := func(state string) onvif.EventNotification {
		return onvif.EventNotification{
			Topic:        "tns1:RuleEngine/CellMotionDetector/Motion",
			Data:         map[string]string{"IsMotion": state},
			PropertyOper: "Changed",
		}
	}
	require.True(t, f.shouldEmit(ev("true")))
	require.False(t, f.shouldEmit(ev("true")), "repeated true suppressed")
	require.False(t, f.shouldEmit(ev("false")))
	require.True(t, f.shouldEmit(ev("true")), "edge re-arms after false")
}

type fakeMgr struct {
	mu      sync.Mutex
	added   []onvif.AddSubscriptionInput
	removed []string
}

func (f *fakeMgr) AddSubscription(_ context.Context, in onvif.AddSubscriptionInput) (*onvif.SubscriptionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, in)
	return &onvif.SubscriptionRecord{ID: "sub-1", CameraID: in.CameraID}, nil
}

func (f *fakeMgr) RemoveSubscription(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}

func TestAdapterSubscribesAndEmits(t *testing.T) {
	mgr := &fakeMgr{}
	disp := NewDispatcher()

	a := &Adapter{
		CameraID: "cam-1",
		XAddr:    "http://192.0.2.1/onvif/event_service",
		Credentials: func(context.Context) (string, string, error) {
			return "admin", "pw", nil
		},
		Mgr:  mgr,
		Disp: disp,
	}

	var mu sync.Mutex
	var got []vendorevents.NormalizedEvent
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, func(ne vendorevents.NormalizedEvent) {
			mu.Lock()
			got = append(got, ne)
			mu.Unlock()
		})
	}()

	// Wait for the subscription, then dispatch notifications: one for
	// this camera, one for another camera (must be ignored).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		n := len(mgr.added)
		mgr.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	disp.Dispatch(onvif.EventNotification{
		SourceCameraID: "cam-1",
		Topic:          "tns1:RuleEngine/CellMotionDetector/Motion",
		Data:           map[string]string{"IsMotion": "true"},
		PropertyOper:   "Changed",
	})
	disp.Dispatch(onvif.EventNotification{
		SourceCameraID: "cam-other",
		Topic:          "tns1:RuleEngine/CellMotionDetector/Motion",
		Data:           map[string]string{"IsMotion": "true"},
		PropertyOper:   "Changed",
	})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	require.Len(t, got, 1)
	require.Equal(t, "motion", got[0].TypeID)
	mu.Unlock()

	mgr.mu.Lock()
	require.Len(t, mgr.added, 1)
	require.Equal(t, "cam-1", mgr.added[0].CameraID)
	require.Equal(t, []string{"sub-1"}, mgr.removed, "subscription torn down on stop")
	mgr.mu.Unlock()
}
