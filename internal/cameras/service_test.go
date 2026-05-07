package cameras

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "cams.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func openVault(t *testing.T) *cameracred.Vault {
	t.Helper()
	v, err := cameracred.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return v
}

func TestService_InsertGetUpdate(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)

	cam := &store.Camera{
		ID: "cam-1", Name: "front_door", DisplayName: "Front Door",
		SourceType: "rtsp", SourceURL: "rtsp://192.168.1.42/cam",
		Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Insert(context.Background(), cam, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := svc.Get(context.Background(), "cam-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "front_door" {
		t.Errorf("got name %q", got.Name)
	}

	got.DisplayName = "Front Door Updated"
	if err := svc.Update(context.Background(), got); err != nil {
		t.Fatalf("update: %v", err)
	}
}

func TestService_MaterializeRTSPURL_NoCredsReturnsTemplate(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)

	cam := &store.Camera{
		ID: "cam-2", Name: "porch", SourceType: "rtsp",
		SourceURL: "rtsp://10.0.0.5/stream",
		Enabled:   true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Insert(context.Background(), cam, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := svc.MaterializeRTSPURL(context.Background(), "cam-2")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got != "rtsp://10.0.0.5/stream" {
		t.Errorf("got %q, want template unchanged", got)
	}
}

func TestService_MaterializeRTSPURL_WithCreds(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)

	cam := &store.Camera{
		ID: "cam-3", Name: "yard", SourceType: "rtsp",
		SourceURL: "rtsp://10.0.0.6/stream",
		Enabled:   true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Insert(context.Background(), cam, "admin", "p4ss"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := svc.MaterializeRTSPURL(context.Background(), "cam-3")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got != "rtsp://admin:p4ss@10.0.0.6/stream" {
		t.Errorf("got %q", got)
	}
}

func TestService_BusEmitsCRUDInOrder(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)

	ch, unsub := svc.Subscribe()
	defer unsub()

	cam := &store.Camera{
		ID: "cam-4", Name: "bus_test", SourceType: "rtsp",
		SourceURL: "rtsp://x/y",
		Enabled:   true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Insert(context.Background(), cam, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	cam.DisplayName = "X"
	if err := svc.Update(context.Background(), cam); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := svc.Delete(context.Background(), "cam-4"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var got []ChangeType
	for i := 0; i < 3; i++ {
		select {
		case c := <-ch:
			got = append(got, c.Type)
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for change %d", i)
		}
	}
	want := []ChangeType{ChangeCreated, ChangeUpdated, ChangeDeleted}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("change[%d] = %v, want %v", i, got[i], w)
		}
	}
}

func TestService_SetCredentialsRequiresUsername(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)
	if err := svc.SetCredentials(context.Background(), "cam-x", "", "p"); err == nil {
		t.Errorf("expected error on empty username")
	}
}

func TestService_HealthMissingReturnsNil(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)
	got, err := svc.Health(context.Background(), "no-such-camera")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("got non-nil snapshot for missing camera: %+v", got)
	}
}

// fakePathManager records every ReloadFromCameras call.
type fakePathManager struct {
	calls [][]CameraPathSpec
}

func (f *fakePathManager) ReloadFromCameras(_ context.Context, cameras []CameraPathSpec) error {
	cp := make([]CameraPathSpec, len(cameras))
	copy(cp, cameras)
	f.calls = append(f.calls, cp)
	return nil
}

func TestPathBridge_BootstrapAndChanges(t *testing.T) {
	s := openStore(t)
	v := openVault(t)
	bus := NewBus()
	svc := NewService(s, v, bus, nil)
	pm := &fakePathManager{}
	br := NewPathBridge(svc, pm, nil)

	// Seed two cameras: one enabled, one disabled.
	now := time.Now().UTC()
	c1 := &store.Camera{ID: "cam-A", Name: "cam-A", SourceType: "rtsp",
		SourceURL: "rtsp://1.1.1.1/s", Enabled: true, CreatedAt: now, UpdatedAt: now}
	c2 := &store.Camera{ID: "cam-B", Name: "cam-B", SourceType: "rtsp",
		SourceURL: "rtsp://2.2.2.2/s", Enabled: false, CreatedAt: now, UpdatedAt: now}
	if err := s.Cameras.Insert(context.Background(), c1); err != nil {
		t.Fatalf("seed c1: %v", err)
	}
	if err := s.Cameras.Insert(context.Background(), c2); err != nil {
		t.Fatalf("seed c2: %v", err)
	}

	if err := br.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if len(pm.calls) != 1 || len(pm.calls[0]) != 1 || pm.calls[0][0].Name != "cam-A" {
		t.Fatalf("bootstrap reload: got %v", pm.calls)
	}

	// Run the bridge in the background and feed it changes via the bus.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { br.Run(ctx); close(done) }()

	// Insert a third enabled camera.
	c3 := &store.Camera{ID: "cam-C", Name: "cam-C", SourceType: "rtsp",
		SourceURL: "rtsp://3.3.3.3/s", Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := svc.Insert(context.Background(), c3, "", ""); err != nil {
		t.Fatalf("insert c3: %v", err)
	}

	// Wait until pm sees both cam-A + cam-C.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if names := lastNames(pm); len(names) == 2 && names[0] == "cam-A" && names[1] == "cam-C" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if names := lastNames(pm); !(len(names) == 2 && names[0] == "cam-A" && names[1] == "cam-C") {
		t.Errorf("after insert: last reload = %v", names)
	}

	// Delete cam-A.
	if err := svc.Delete(context.Background(), "cam-A"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if names := lastNames(pm); len(names) == 1 && names[0] == "cam-C" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if names := lastNames(pm); !(len(names) == 1 && names[0] == "cam-C") {
		t.Errorf("after delete: last reload = %v", names)
	}

	cancel()
	<-done
}

func lastNames(pm *fakePathManager) []string {
	if len(pm.calls) == 0 {
		return nil
	}
	last := pm.calls[len(pm.calls)-1]
	out := make([]string, len(last))
	for i, s := range last {
		out[i] = s.Name
	}
	sort.Strings(out)
	return out
}
