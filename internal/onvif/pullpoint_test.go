package onvif

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const createPullPointFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
              xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">
  <env:Body>
    <tev:CreatePullPointSubscriptionResponse>
      <tev:SubscriptionReference>
        <wsa:Address>http://camera.example.com/onvif/Subscription?Idx=42</wsa:Address>
      </tev:SubscriptionReference>
      <tev:CurrentTime>2026-05-06T10:00:00Z</tev:CurrentTime>
      <tev:TerminationTime>2026-05-06T10:05:00Z</tev:TerminationTime>
    </tev:CreatePullPointSubscriptionResponse>
  </env:Body>
</env:Envelope>`

const pullMessagesMotionFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
              xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
              xmlns:tt="http://www.onvif.org/ver10/schema">
  <env:Body>
    <tev:PullMessagesResponse>
      <tev:CurrentTime>2026-05-06T10:00:30Z</tev:CurrentTime>
      <tev:TerminationTime>2026-05-06T10:05:30Z</tev:TerminationTime>
      <wsnt:NotificationMessage>
        <wsnt:Topic Dialect="http://docs.oasis-open.org/wsn/t-1/TopicExpression/Simple">tns1:VideoSource/MotionAlarm</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-05-06T10:00:30Z" PropertyOperation="Changed">
            <tt:Source>
              <tt:SimpleItem Name="VideoSourceConfigurationToken" Value="VideoSource_1"/>
            </tt:Source>
            <tt:Data>
              <tt:SimpleItem Name="State" Value="true"/>
            </tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </env:Body>
</env:Envelope>`

const pullMessagesEmptyFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:tev="http://www.onvif.org/ver10/events/wsdl">
  <env:Body>
    <tev:PullMessagesResponse>
      <tev:CurrentTime>2026-05-06T10:01:00Z</tev:CurrentTime>
      <tev:TerminationTime>2026-05-06T10:06:00Z</tev:TerminationTime>
    </tev:PullMessagesResponse>
  </env:Body>
</env:Envelope>`

const renewResponseFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2">
  <env:Body>
    <wsnt:RenewResponse>
      <wsnt:CurrentTime>2026-05-06T10:02:00Z</wsnt:CurrentTime>
      <wsnt:TerminationTime>2026-05-06T10:07:00Z</wsnt:TerminationTime>
    </wsnt:RenewResponse>
  </env:Body>
</env:Envelope>`

func TestPullPointCreate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "CreatePullPointSubscription")
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, createPullPointFixture)
	}))
	defer srv.Close()

	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	sub, err := client.Create(context.Background())
	require.NoError(t, err)
	require.Equal(t, "http://camera.example.com/onvif/Subscription?Idx=42", sub.URL)
	require.False(t, sub.TerminationTime.IsZero())
}

func TestPullPointPull_ParsesEvents(t *testing.T) {
	called := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, pullMessagesMotionFixture)
	}))
	defer srv.Close()

	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	events, err := client.Pull(context.Background(), &Subscription{URL: srv.URL, CameraID: "cam-1"})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "tns1:VideoSource/MotionAlarm", events[0].Topic)
	require.Equal(t, "cam-1", events[0].SourceCameraID)
	require.Equal(t, "Changed", events[0].PropertyOper)
	require.Equal(t, "true", events[0].Data["data.State"])
	require.Equal(t, "VideoSource_1", events[0].Data["source.VideoSourceConfigurationToken"])
	require.Equal(t, int32(1), atomic.LoadInt32(&called))
}

func TestPullPointPull_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, pullMessagesEmptyFixture)
	}))
	defer srv.Close()

	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	events, err := client.Pull(context.Background(), &Subscription{URL: srv.URL})
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestPullPointRenew_UpdatesTerminationTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "Renew")
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, renewResponseFixture)
	}))
	defer srv.Close()

	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	original := &Subscription{
		URL:             srv.URL,
		TerminationTime: time.Date(2026, 5, 6, 10, 5, 0, 0, time.UTC),
	}
	renewed, err := client.Renew(context.Background(), original)
	require.NoError(t, err)
	require.True(t, renewed.TerminationTime.After(original.TerminationTime))
}

func TestPullPointUnsubscribe_Idempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.Contains(t, string(body), "Unsubscribe")
		w.Header().Set("Content-Type", `application/soap+xml`)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><wsnt:UnsubscribeResponse xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"/></env:Body></env:Envelope>`)
	}))
	defer srv.Close()
	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	require.NoError(t, client.Unsubscribe(context.Background(), &Subscription{URL: srv.URL}))
}

func TestParsePullResponse_HandlesMultipleMessages(t *testing.T) {
	multi := `<?xml version="1.0" encoding="UTF-8"?>
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
          <tt:Message UtcTime="2026-05-06T10:00:30Z" PropertyOperation="Initialized"/>
        </wsnt:Message>
      </wsnt:NotificationMessage>
      <wsnt:NotificationMessage>
        <wsnt:Topic>tns1:Device/Trigger/DigitalInput</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-05-06T10:00:31Z" PropertyOperation="Changed">
            <tt:Data><tt:SimpleItem Name="LogicalState" Value="false"/></tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </env:Body>
</env:Envelope>`
	events, _, _, err := parsePullResponse([]byte(multi))
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "tns1:VideoSource/MotionAlarm", events[0].Topic)
	require.Equal(t, "tns1:Device/Trigger/DigitalInput", events[1].Topic)
	require.Equal(t, "false", events[1].Data["data.LogicalState"])
}

func TestPullPointSOAPFault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, soapFaultFixture)
	}))
	defer srv.Close()
	client := &PullPointClient{XAddr: srv.URL, HTTPClient: srv.Client(), PullTimeout: 1 * time.Second}
	_, err := client.Create(context.Background())
	require.Error(t, err)
	var f *SOAPFault
	require.ErrorAs(t, err, &f)
}

func TestBuildCreateEnvelope_CarriesAuth(t *testing.T) {
	c := &PullPointClient{Username: "admin", Password: "pw", Now: func() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }}
	env := c.buildCreateEnvelope(60 * time.Second)
	s := string(env)
	require.True(t, strings.Contains(s, "<wsse:Security"))
	require.True(t, strings.Contains(s, "admin"))
}

func TestBuildPullEnvelope_TimeoutAndLimitInterpolated(t *testing.T) {
	c := &PullPointClient{}
	env := c.buildPullEnvelope(45*time.Second, 50)
	s := string(env)
	require.Contains(t, s, "PT45S")
	require.Contains(t, s, "<tev:MessageLimit>50</tev:MessageLimit>")
}
