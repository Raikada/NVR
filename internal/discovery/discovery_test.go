package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/store"
)

type fakeProber struct {
	devices []onvif.DiscoveredDevice
	err     error
	calls   int
}

func (f *fakeProber) Probe(_ context.Context) ([]onvif.DiscoveredDevice, error) {
	f.calls++
	return f.devices, f.err
}

type fakeLister struct{ cams []*store.Camera }

func (f *fakeLister) List(_ context.Context, _ store.ListCamerasFilter) ([]*store.Camera, error) {
	return f.cams, nil
}

func dev(ref, xaddr, model string) onvif.DiscoveredDevice {
	return onvif.DiscoveredDevice{EndpointRef: ref, XAddr: xaddr, Model: model}
}

func newTestService(p Prober, l CameraLister, now *time.Time) *Service {
	s := New(p, l, nil)
	s.clock = func() time.Time { return *now }
	return s
}

func TestProbeNowCachesAndPreservesFirstSeen(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{
		dev("urn:uuid:aaa", "http://192.168.1.110/onvif/device_service", "IP5M"),
	}}
	s := newTestService(p, &fakeLister{}, &now)

	_, err := s.ProbeNow(context.Background())
	require.NoError(t, err)
	first := s.Snapshot()
	require.Len(t, first, 1)
	require.Equal(t, now, first[0].FirstSeenAt)

	// Second round 30s later: FirstSeenAt sticks, LastSeenAt advances.
	now = now.Add(30 * time.Second)
	_, err = s.ProbeNow(context.Background())
	require.NoError(t, err)
	entries := s.Snapshot()
	require.Len(t, entries, 1)
	require.Equal(t, first[0].FirstSeenAt, entries[0].FirstSeenAt)
	require.Equal(t, now, entries[0].LastSeenAt)
}

func TestStaleEntriesAgeOut(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{dev("urn:uuid:aaa", "http://a/onvif", "A")}}
	s := newTestService(p, &fakeLister{}, &now)

	_, _ = s.ProbeNow(context.Background())
	require.Len(t, s.Snapshot(), 1)

	// Device disappears; 11 minutes pass — entry ages out on the next round.
	p.devices = nil
	now = now.Add(11 * time.Minute)
	_, _ = s.ProbeNow(context.Background())
	require.Empty(t, s.Snapshot())
}

func TestMatchingFlagsManagedCameras(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{
		dev("urn:uuid:managed", "http://192.168.1.110/onvif/device_service", "IP5M"),
		dev("urn:uuid:new", "http://192.168.1.50/onvif/device_service", "AD410"),
	}}
	l := &fakeLister{cams: []*store.Camera{{
		ID:        "cam-1",
		Name:      "front",
		SourceURL: "rtsp://192.168.1.110:554/cam/realmonitor?channel=1&subtype=0",
	}}}
	s := newTestService(p, l, &now)

	_, err := s.ProbeNow(context.Background())
	require.NoError(t, err)

	byRef := map[string]Entry{}
	for _, e := range s.Snapshot() {
		byRef[e.EndpointRef] = e
	}
	require.Equal(t, "cam-1", byRef["urn:uuid:managed"].MatchedCameraID)
	require.Empty(t, byRef["urn:uuid:new"].MatchedCameraID)
}

func TestMatchingByOnvifXAddr(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{
		dev("urn:uuid:x", "http://192.168.1.111:8000/onvif/device_service", "AD410"),
	}}
	l := &fakeLister{cams: []*store.Camera{{
		ID:         "cam-2",
		Name:       "doorbell",
		SourceURL:  "rtsp://door.example:554/stream",
		OnvifXAddr: "http://192.168.1.111/onvif/device_service",
	}}}
	s := newTestService(p, l, &now)

	_, err := s.ProbeNow(context.Background())
	require.NoError(t, err)
	entries := s.Snapshot()
	require.Len(t, entries, 1)
	require.Equal(t, "cam-2", entries[0].MatchedCameraID)
}

func TestProbeFailureKeepsStaleCache(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{dev("urn:uuid:aaa", "http://a/onvif", "A")}}
	s := newTestService(p, &fakeLister{}, &now)

	_, _ = s.ProbeNow(context.Background())
	require.Len(t, s.Snapshot(), 1)

	p.err = errors.New("multicast filtered")
	now = now.Add(time.Minute)
	_, err := s.ProbeNow(context.Background())
	require.Error(t, err)
	require.Len(t, s.Snapshot(), 1, "stale cache must survive a failed probe")
}

func TestLookup(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	p := &fakeProber{devices: []onvif.DiscoveredDevice{dev("urn:uuid:aaa", "http://a/onvif", "A")}}
	s := newTestService(p, &fakeLister{}, &now)
	_, _ = s.ProbeNow(context.Background())

	_, ok := s.Lookup("urn:uuid:aaa")
	require.True(t, ok)
	_, ok = s.Lookup("urn:uuid:zzz")
	require.False(t, ok)
}
