// Package cameras: Phase A B7 — substream path registration.
package cameras

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/store"
)

type captivePM struct{ last []CameraPathSpec }

func (c *captivePM) ReloadFromCameras(_ context.Context, specs []CameraPathSpec) error {
	c.last = specs
	return nil
}

func TestPathBridgeRegistersSubStream(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)

	cam := &store.Camera{
		ID: "cam-sub", Name: "subcam", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1:554/main", Enabled: true,
	}
	if err := svc.Insert(context.Background(), cam, "alice", "pw1"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	vendor, _ := json.Marshal(map[string]string{
		"sub_stream_uri": "rtsp://192.0.2.1:554/sub",
	})
	if err := s.CameraCapabilities.Upsert(context.Background(), &store.CameraCapabilities{
		CameraID: "cam-sub", ProfilesJSON: "[]", SelectedProfileToken: "p0",
		VendorCapabilitiesJSON: string(vendor), ProbedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("caps: %v", err)
	}

	pm := &captivePM{}
	b := NewPathBridge(svc, pm, nil)
	if err := b.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if len(pm.last) != 2 {
		t.Fatalf("expected main+sub specs, got %d: %+v", len(pm.last), pm.last)
	}
	byName := map[string]CameraPathSpec{}
	for _, sp := range pm.last {
		byName[sp.Name] = sp
	}
	main, ok := byName["subcam"]
	if !ok || main.NoRecord {
		t.Fatalf("main path missing or not recording: %+v", byName)
	}
	sub, ok := byName["subcam_sub"]
	if !ok {
		t.Fatalf("sub path missing: %+v", byName)
	}
	if !sub.NoRecord {
		t.Fatal("sub path must not record")
	}
	// Credentials materialize into the sub URL too.
	if want := "rtsp://alice:pw1@192.0.2.1:554/sub"; sub.SourceURL != want {
		t.Fatalf("sub url = %q, want %q", sub.SourceURL, want)
	}
}

func TestPathBridgeNoSubWithoutCapability(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	svc := NewService(s, v, NewBus(), nil)
	cam := &store.Camera{
		ID: "cam-nosub", Name: "nosub", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.2:554/main", Enabled: true,
	}
	if err := svc.Insert(context.Background(), cam, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	pm := &captivePM{}
	b := NewPathBridge(svc, pm, nil)
	if err := b.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(pm.last) != 1 {
		t.Fatalf("expected only the main spec, got %d", len(pm.last))
	}
}
