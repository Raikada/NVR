package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/onvif"
)

// invokeOnvifHandler runs an ONVIF handler via a synthetic gin.Context.
// Mirrors invokeCameraHandler in shape; we don't go through
// api.Initialize() because the routes are gated and the test focus is
// the handler logic.
func invokeOnvifHandler(
	api *API,
	handler func(*gin.Context),
	method, target, idParam string,
	body []byte,
) (int, []byte) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	var bodyR io.Reader
	if body != nil {
		bodyR = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, bodyR)
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	handler(c)
	return w.Code, w.Body.Bytes()
}

func TestV1OnvifDiscover_RunsAndReturnsList(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{"timeout_ms": 100})
	require.NoError(t, err)

	code, respBody := invokeOnvifHandler(api, api.onV1OnvifDiscover, http.MethodPost,
		"/v1/onvif/discover", "", body)
	require.Equal(t, http.StatusOK, code)

	var resp onvifDiscoverResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))
	// We can't assert specific cameras (depends on LAN); just ensure
	// the field is present and serializes as a list.
	require.NotNil(t, resp.Devices)
}

func TestV1OnvifDeviceInfo_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
  <env:Body>
    <tds:GetDeviceInformationResponse>
      <tds:Manufacturer>TestVendor</tds:Manufacturer>
      <tds:Model>TestModel</tds:Model>
      <tds:FirmwareVersion>1.0</tds:FirmwareVersion>
      <tds:SerialNumber>TST123</tds:SerialNumber>
      <tds:HardwareId>HW1</tds:HardwareId>
    </tds:GetDeviceInformationResponse>
  </env:Body>
</env:Envelope>`)
	}))
	defer srv.Close()

	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	body, err := json.Marshal(map[string]any{"xaddr": srv.URL})
	require.NoError(t, err)

	code, respBody := invokeOnvifHandler(api, api.onV1OnvifDeviceInfo, http.MethodPost,
		"/v1/onvif/device-info", "", body)
	require.Equal(t, http.StatusOK, code)

	var info onvif.DeviceInformation
	require.NoError(t, json.Unmarshal(respBody, &info))
	require.Equal(t, "TestVendor", info.Manufacturer)
	require.Equal(t, "TestModel", info.Model)
}

func TestV1OnvifDeviceInfo_RejectsMissingXAddr(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}
	body, err := json.Marshal(map[string]any{})
	require.NoError(t, err)
	code, _ := invokeOnvifHandler(api, api.onV1OnvifDeviceInfo, http.MethodPost,
		"/v1/onvif/device-info", "", body)
	require.Equal(t, http.StatusBadRequest, code)
}

// TestV1OnvifEventSubscriptions_LifecycleEmitsEvent exercises the full
// subscription flow: POST creates, the manager pulls a real event from
// a fake camera, the event lands in the EventStore, GET lists, DELETE
// removes.
func TestV1OnvifEventSubscriptions_LifecycleEmitsEvent(t *testing.T) {
	resetEventStoreSingleton(t)

	// Fake camera with known shape: first call is Create, follow-up
	// calls are PullMessages with one motion event.
	calls := int32(0)
	respMu := sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Content-Type", "application/soap+xml")
		respMu.Lock()
		calls++
		respMu.Unlock()
		switch {
		case strings.Contains(s, "CreatePullPointSubscription"):
			// Subscription URL points back at this same server.
			fixture := strings.Replace(`<?xml version="1.0"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
              xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">
  <env:Body>
    <tev:CreatePullPointSubscriptionResponse>
      <tev:SubscriptionReference>
        <wsa:Address>__URL__/sub</wsa:Address>
      </tev:SubscriptionReference>
      <tev:CurrentTime>2026-05-06T10:00:00Z</tev:CurrentTime>
      <tev:TerminationTime>2026-05-06T10:05:00Z</tev:TerminationTime>
    </tev:CreatePullPointSubscriptionResponse>
  </env:Body>
</env:Envelope>`, "__URL__", "http://"+r.Host, 1)
			_, _ = io.WriteString(w, fixture)
		case strings.Contains(s, "PullMessages"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
              xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
              xmlns:tt="http://www.onvif.org/ver10/schema">
  <env:Body>
    <tev:PullMessagesResponse>
      <tev:CurrentTime>2026-05-06T10:00:30Z</tev:CurrentTime>
      <tev:TerminationTime>2026-05-06T10:05:30Z</tev:TerminationTime>
      <wsnt:NotificationMessage>
        <wsnt:Topic>tns1:VideoSource/MotionAlarm</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-05-06T10:00:30Z" PropertyOperation="Changed">
            <tt:Source><tt:SimpleItem Name="Token" Value="VideoSource_1"/></tt:Source>
            <tt:Data><tt:SimpleItem Name="State" Value="true"/></tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </env:Body>
</env:Envelope>`)
		case strings.Contains(s, "Unsubscribe"):
			_, _ = io.WriteString(w, `<?xml version="1.0"?><env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><wsnt:UnsubscribeResponse xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"/></env:Body></env:Envelope>`)
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	// Inject a manager wired against the test event store and the
	// fake camera.
	mgr := onvif.NewManager(nil, func(ev onvif.EventNotification) {
		PublishOnvifEvent(ev)
	}, srv.Client())
	mgr.PullTimeout = 200 * time.Millisecond
	mgr.SubscriptionDuration = 60 * time.Second
	SetOnvifManager(mgr)
	t.Cleanup(func() {
		SetOnvifManager(nil)
		mgr.Close()
	})

	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}

	camID := uuid.New().String()
	body, err := json.Marshal(map[string]any{
		"camera_id": camID,
		"xaddr":     srv.URL,
	})
	require.NoError(t, err)

	// POST creates.
	code, respBody := invokeOnvifHandler(api, api.onV1OnvifEventSubscriptionsPost,
		http.MethodPost, "/v1/onvif/event-subscriptions", "", body)
	require.Equal(t, http.StatusCreated, code, "body=%s", string(respBody))
	var rec onvif.SubscriptionRecord
	require.NoError(t, json.Unmarshal(respBody, &rec))
	require.NotEmpty(t, rec.ID)
	require.Equal(t, camID, rec.CameraID)

	// Wait for the manager to pull at least one event from the fake
	// camera and translate it into an Event.
	deadline := time.Now().Add(3 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		for _, e := range defaultEventStore().Snapshot() {
			if string(e.Kind) == "camera.motion_detected" {
				found = true
				require.Equal(t, camID, e.SubjectID)
				require.Equal(t, "info", string(e.Severity))
				require.Equal(t, "tns1:VideoSource/MotionAlarm", e.Attributes["onvif_topic"])
				break
			}
		}
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, found, "expected a camera.motion_detected event in the store")

	// GET lists.
	code, respBody = invokeOnvifHandler(api, api.onV1OnvifEventSubscriptionsList,
		http.MethodGet, "/v1/onvif/event-subscriptions", "", nil)
	require.Equal(t, http.StatusOK, code)
	var list onvifEventSubscriptionsList
	require.NoError(t, json.Unmarshal(respBody, &list))
	require.Len(t, list.Items, 1)

	// DELETE removes.
	code, _ = invokeOnvifHandler(api, api.onV1OnvifEventSubscriptionsDelete,
		http.MethodDelete, "/v1/onvif/event-subscriptions/"+rec.ID, rec.ID, nil)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, mgr.List())
}

// TestV1OnvifEventSubscriptions_RejectsInvalidCameraID confirms the
// camera_id is validated as a UUID.
func TestV1OnvifEventSubscriptions_RejectsInvalidCameraID(t *testing.T) {
	cnf := tempConf(t, "api: yes\n")
	api := &API{Conf: cnf, Parent: &testParent{}}
	body, err := json.Marshal(map[string]any{
		"camera_id": "not-a-uuid",
		"xaddr":     "http://example.com",
	})
	require.NoError(t, err)
	code, _ := invokeOnvifHandler(api, api.onV1OnvifEventSubscriptionsPost,
		http.MethodPost, "/v1/onvif/event-subscriptions", "", body)
	require.Equal(t, http.StatusBadRequest, code)
}

// TestPublishOnvifEvent_UnknownTopicFallsThrough verifies the
// generic fall-through for unknown ONVIF topics.
func TestPublishOnvifEvent_UnknownTopicFallsThrough(t *testing.T) {
	resetEventStoreSingleton(t)
	store := NewEventStore(0)
	const tenantID = "11111111-2222-3333-4444-555555555555"
	SetPipelineEventTarget(store, func() string { return tenantID })
	t.Cleanup(func() { SetPipelineEventTarget(nil, nil) })

	camID := uuid.New().String()
	publishOnvifEvent(onvif.EventNotification{
		Topic:          "tns1:Some/Vendor/Specific/Thing",
		SourceCameraID: camID,
		Data:           map[string]string{"data.foo": "bar"},
	})
	require.Equal(t, 1, store.Len())
	got := store.Snapshot()[0]
	require.Equal(t, "camera.onvif_event", string(got.Kind))
	require.Equal(t, camID, got.SubjectID)
	require.Equal(t, tenantID, got.TenantID)
	require.Equal(t, "tns1:Some/Vendor/Specific/Thing", got.Attributes["onvif_topic"])
	require.Equal(t, "bar", got.Attributes["data.foo"])
}
