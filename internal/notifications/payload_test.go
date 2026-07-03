package notifications

import (
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/store"
)

func TestBuild(t *testing.T) {
	ev := &events.Event{
		ID: "ev-1", CameraID: "cam-1", TypeID: "motion", Source: "onvif_pullpoint",
		OccurredAt: time.Now(), ReceivedAt: time.Now(), Severity: "info",
	}
	cam := &store.Camera{ID: "cam-1", Name: "front_door", DisplayName: "Front Door"}
	evType := &store.EventType{ID: "motion", DisplayName: "Motion"}
	site := SitePayload{ID: "site-1", Name: "My House"}
	p := Build("d-1", site, ev, cam, evType)
	if p.Schema != PayloadSchema {
		t.Errorf("Schema = %q, want %q", p.Schema, PayloadSchema)
	}
	if p.DeliveryID != "d-1" {
		t.Errorf("DeliveryID = %q, want d-1", p.DeliveryID)
	}
	if p.Event.Camera.ID != "cam-1" {
		t.Errorf("Event.Camera.ID = %q, want cam-1", p.Event.Camera.ID)
	}
	if p.Event.Camera.DisplayName != "Front Door" {
		t.Errorf("Event.Camera.DisplayName = %q, want Front Door", p.Event.Camera.DisplayName)
	}
	if p.Event.TypeDisplayName != "Motion" {
		t.Errorf("Event.TypeDisplayName = %q, want Motion", p.Event.TypeDisplayName)
	}
	if p.Site.Name != "My House" {
		t.Errorf("Site.Name = %q", p.Site.Name)
	}
}

func TestRenderEmail(t *testing.T) {
	p := &Payload{
		Schema:     PayloadSchema,
		DeliveryID: "d-1",
		Site:       SitePayload{ID: "site-1", Name: "My House"},
		Event: EventPayload{
			ID: "ev-1", Type: "motion", TypeDisplayName: "Motion",
			Camera: CameraPayload{ID: "cam-1", Name: "front_door", DisplayName: "Front Door"},
			Source: "onvif_pullpoint", Severity: "info",
			OccurredAt: time.Now(), ReceivedAt: time.Now(),
		},
		ThumbnailURL: "https://example/thumb",
		SnapshotURL:  "https://example/snapshot",
	}
	subject, body, err := RenderEmail(p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if subject == "" || body == "" {
		t.Errorf("empty subject/body: subject=%q body=%q", subject, body)
	}
	// Confirm the subject includes site + display names
	for _, want := range []string{"My House", "Motion", "Front Door"} {
		if !contains(subject, want) {
			t.Errorf("subject %q missing %q", subject, want)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
