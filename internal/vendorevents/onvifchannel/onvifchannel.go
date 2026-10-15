// Package onvifchannel implements the ONVIF PullPoint vendor event
// channel (SP3). The shared onvif.Manager owns subscriptions, renewal
// and the pull loop; this package fans its notification sink out per
// camera (Dispatcher) and normalizes topics onto the seeded
// event_types vocabulary.
package onvifchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

// SubscriptionManager is the onvif.Manager subset the adapter uses.
type SubscriptionManager interface {
	AddSubscription(ctx context.Context, in onvif.AddSubscriptionInput) (*onvif.SubscriptionRecord, error)
	RemoveSubscription(ctx context.Context, id string) error
}

// Dispatcher fans the manager's single notification sink out to
// per-camera subscribers. Core wires manager sink → Dispatch.
type Dispatcher struct {
	mu   sync.Mutex
	subs map[string][]chan onvif.EventNotification // camera id → subscriber channels
}

// NewDispatcher returns an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{subs: map[string][]chan onvif.EventNotification{}}
}

// Dispatch routes one notification to its camera's subscribers.
// Non-blocking: a stuck subscriber drops its own events.
func (d *Dispatcher) Dispatch(ev onvif.EventNotification) {
	d.mu.Lock()
	chans := d.subs[ev.SourceCameraID]
	d.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe registers a per-camera notification channel; the returned
// func unsubscribes.
func (d *Dispatcher) Subscribe(cameraID string) (<-chan onvif.EventNotification, func()) {
	ch := make(chan onvif.EventNotification, 64)
	d.mu.Lock()
	d.subs[cameraID] = append(d.subs[cameraID], ch)
	d.mu.Unlock()
	return ch, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		list := d.subs[cameraID]
		for i, c := range list {
			if c == ch {
				d.subs[cameraID] = append(list[:i], list[i+1:]...)
				close(c)
				return
			}
		}
	}
}

// Adapter is one camera's ONVIF PullPoint channel.
type Adapter struct {
	CameraID    string
	XAddr       string // events (or device) service URL
	Credentials func(ctx context.Context) (username, password string, err error)
	Mgr         SubscriptionManager
	Disp        *Dispatcher
}

// New builds the adapter from a ChannelCamera; mgr and disp are bound
// by core wiring via a closure (vendorevents.AdapterFactory shape).
func New(mgr SubscriptionManager, disp *Dispatcher) vendorevents.AdapterFactory {
	return func(_ context.Context, cam vendorevents.ChannelCamera) (vendorevents.Adapter, error) {
		xaddr := cam.EventsXAddr
		if xaddr == "" {
			xaddr = cam.OnvifXAddr
		}
		if xaddr == "" {
			return nil, fmt.Errorf("onvif channel: camera %s has no xaddr: %w", cam.ID, vendorevents.ErrUnsupported)
		}
		if cam.Credentials == nil {
			return nil, fmt.Errorf("onvif channel: camera %s has no credentials: %w", cam.ID, vendorevents.ErrUnsupported)
		}
		return &Adapter{
			CameraID:    cam.ID,
			XAddr:       xaddr,
			Credentials: cam.Credentials,
			Mgr:         mgr,
			Disp:        disp,
		}, nil
	}
}

// Run implements vendorevents.Adapter: ensure a subscription, consume
// dispatched notifications, normalize + emit.
func (a *Adapter) Run(ctx context.Context, emit func(vendorevents.NormalizedEvent)) error {
	username, password, err := a.Credentials(ctx)
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	rec, err := a.Mgr.AddSubscription(ctx, onvif.AddSubscriptionInput{
		CameraID: a.CameraID,
		XAddr:    a.XAddr,
		Username: username,
		Password: password,
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer func() {
		// Teardown uses a fresh context: ours is already cancelled on
		// the normal stop path.
		tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Mgr.RemoveSubscription(tctx, rec.ID)
	}()

	ch, unsub := a.Disp.Subscribe(a.CameraID)
	defer unsub()

	edges := newEdgeFilter()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				return fmt.Errorf("dispatcher closed")
			}
			if !edges.shouldEmit(ev) {
				continue
			}
			if ne, emitIt := mapNotification(ev); emitIt {
				emit(ne)
			}
		}
	}
}

// kindToTypeID: canonical ONVIF kind (event_topics.go) → seeded
// event_types id.
var kindToTypeID = map[string]string{
	"camera.motion_detected": "motion",
	"camera.field_detection": "motion",
	"camera.line_crossing":   "line_cross",
	"camera.tamper_detected": "tamper",
	"camera.scene_change":    "tamper",
	"camera.signal_loss":     "tamper",
	"camera.audio_detected":  "audio_alarm",
	"camera.digital_input":   "io_in",
}

// mapNotification normalizes one ONVIF notification. emit=false drops
// it (unknown topic, falling edge, subscribe-time state dump).
func mapNotification(ev onvif.EventNotification) (vendorevents.NormalizedEvent, bool) {
	if ev.PropertyOper == "Initialized" {
		// Subscribe-time state dump, not a new occurrence.
		return vendorevents.NormalizedEvent{}, false
	}
	if s, ok := stateOf(ev.Data); ok && !s {
		return vendorevents.NormalizedEvent{}, false
	}

	mapping, ok := onvif.CanonicalEventKindForTopic(ev.Topic)
	typeID := ""
	if ok {
		typeID = kindToTypeID[mapping.Kind]
	}
	// Classification refines/rescues the mapping: vendor rule topics
	// often carry the object class in the payload.
	switch strings.ToLower(ev.Data["ObjectClass"]) {
	case "human", "person", "people":
		typeID = "person"
	case "vehicle", "car":
		typeID = "vehicle"
	}
	if typeID == "" {
		return vendorevents.NormalizedEvent{}, false
	}

	severity := "info"
	if typeID == "tamper" {
		severity = "warning"
	}
	payload, _ := json.Marshal(map[string]any{
		"topic": ev.Topic,
		"data":  ev.Data,
	})
	occurred := ev.UTCTime
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	return vendorevents.NormalizedEvent{
		TypeID:     typeID,
		Severity:   severity,
		OccurredAt: occurred,
		Payload:    payload,
	}, true
}

// stateOf extracts the boolean property state from notification data.
// Vendors use different keys; absence means "not a property event".
func stateOf(data map[string]string) (bool, bool) {
	for _, key := range []string{"IsMotion", "State", "LogicalState", "state"} {
		if v, ok := data[key]; ok {
			return strings.EqualFold(v, "true") || v == "1", true
		}
	}
	return false, false
}

// edgeFilter suppresses repeated rising states per topic: some cameras
// re-send State=true on every pull while a condition holds.
type edgeFilter struct {
	last map[string]bool // topic → last seen state
}

func newEdgeFilter() *edgeFilter { return &edgeFilter{last: map[string]bool{}} }

// shouldEmit reports whether this notification represents a fresh
// occurrence. Non-property events (no state key) always emit.
func (f *edgeFilter) shouldEmit(ev onvif.EventNotification) bool {
	s, ok := stateOf(ev.Data)
	if !ok {
		return true
	}
	prev := f.last[ev.Topic]
	f.last[ev.Topic] = s
	return s && !prev
}
