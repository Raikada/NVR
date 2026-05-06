// PullPoint event subscription. The recorder subscribes to a camera's
// event service via WS-BaseNotification PullPoint pattern: create a
// subscription, then loop {PullMessages, Renew, Unsubscribe-on-close}.
//
// PullMessages is a long-poll: the camera holds the connection open
// up to `Timeout` waiting for events. Cameras typically support 60-300
// second pulls; we default to 60 seconds so the recorder can wake to
// renew the subscription on the natural cadence rather than racing
// the camera-side TerminationTime.
//
// The recorder issues a Renew before the subscription expires (we
// take TerminationTime - 30 seconds as the renew deadline) so events
// keep flowing without operator intervention.

package onvif

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultPullTimeout is the long-poll deadline. Cameras generally keep
// the connection open up to 60 seconds; longer values risk firewalls
// dropping the connection mid-poll.
const DefaultPullTimeout = 60 * time.Second

// DefaultSubscriptionDuration is the initial subscription window we
// request from the camera. Longer windows reduce Renew traffic; shorter
// windows recover faster from a recorder restart that drops the
// in-memory subscription URL.
const DefaultSubscriptionDuration = 5 * time.Minute

// EventNotification is one parsed NotificationMessage from a PullPoint
// response. Topic is the full ONVIF topic URI (e.g.,
// "tns1:VideoSource/MotionAlarm"); Data carries the event-specific
// fields as a flat map.
type EventNotification struct {
	Topic         string
	UTCTime       time.Time
	Data          map[string]string
	MessageID     string
	PropertyOper  string // Initialized / Changed / Deleted (per WS-BaseNotification)
	SourceCameraID string // populated by the manager from subscription metadata
	Raw           string // raw XML for debugging — small, kept for audit
}

// PullPointClient drives a single subscription against a camera.
// Construction:
//
//	client := &PullPointClient{XAddr: "http://cam/onvif/event_service", Username: ..., Password: ...}
//	sub, err := client.Create(ctx)
//	for {
//	    events, err := client.Pull(ctx, sub)
//	    ...
//	    if approachingExpiry(sub) {
//	        sub, _ = client.Renew(ctx, sub)
//	    }
//	}
//	_ = client.Unsubscribe(ctx, sub)
type PullPointClient struct {
	XAddr      string
	Username   string
	Password   string
	HTTPClient *http.Client
	Now        func() time.Time
	NewNonce   func() string
	// PullTimeout is the per-PullMessages long-poll deadline. Zero =
	// DefaultPullTimeout.
	PullTimeout time.Duration
	// SubscriptionDuration is the lifetime requested from the camera at
	// Create / Renew. Zero = DefaultSubscriptionDuration.
	SubscriptionDuration time.Duration
}

// Subscription is the camera-side handle. URL is the per-subscription
// endpoint the camera returned in CreatePullPointSubscriptionResponse;
// Renew / PullMessages / Unsubscribe go to this URL, NOT the original
// event service URL.
type Subscription struct {
	URL                string
	CurrentTime        time.Time
	TerminationTime    time.Time
	CameraID           string
}

// Create issues CreatePullPointSubscription and returns a Subscription
// containing the camera-issued endpoint URL + termination time.
func (c *PullPointClient) Create(ctx context.Context) (*Subscription, error) {
	dur := c.SubscriptionDuration
	if dur <= 0 {
		dur = DefaultSubscriptionDuration
	}
	envelope := c.buildCreateEnvelope(dur)
	body, err := c.doSOAP(ctx, c.XAddr, ActionCreatePullPointSubscript, envelope)
	if err != nil {
		return nil, err
	}
	var env createSubscriptionEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		if f := parseSOAPFault(body); f != nil {
			return nil, f
		}
		return nil, fmt.Errorf("decode subscription response: %w", err)
	}
	if env.Body.Fault != nil {
		return nil, &SOAPFault{
			Code:   env.Body.Fault.Code.Value,
			Reason: env.Body.Fault.Reason.Text,
		}
	}
	addr := strings.TrimSpace(env.Body.Response.SubscriptionReference.Address)
	if addr == "" {
		return nil, fmt.Errorf("subscription reference URL missing in response")
	}
	sub := &Subscription{
		URL:             addr,
		CurrentTime:     parseTimeOrZero(env.Body.Response.CurrentTime),
		TerminationTime: parseTimeOrZero(env.Body.Response.TerminationTime),
	}
	return sub, nil
}

// Pull issues PullMessages against an existing subscription URL and
// returns parsed events. Uses long-polling: the call blocks until the
// camera has events to deliver or the timeout elapses.
//
// Empty result (no events in the polling window) is a success; we just
// return an empty slice.
func (c *PullPointClient) Pull(ctx context.Context, sub *Subscription) ([]EventNotification, error) {
	timeout := c.PullTimeout
	if timeout <= 0 {
		timeout = DefaultPullTimeout
	}
	envelope := c.buildPullEnvelope(timeout, 100)
	body, err := c.doSOAP(ctx, sub.URL, ActionPullMessages, envelope)
	if err != nil {
		return nil, err
	}
	if f := parseSOAPFault(body); f != nil {
		return nil, f
	}
	events, _, _, err := parsePullResponse(body)
	if err != nil {
		return nil, err
	}
	for i := range events {
		events[i].SourceCameraID = sub.CameraID
	}
	return events, nil
}

// Renew extends the subscription. On success, the returned Subscription
// has updated CurrentTime + TerminationTime; URL is preserved.
func (c *PullPointClient) Renew(ctx context.Context, sub *Subscription) (*Subscription, error) {
	dur := c.SubscriptionDuration
	if dur <= 0 {
		dur = DefaultSubscriptionDuration
	}
	envelope := c.buildRenewEnvelope(dur)
	body, err := c.doSOAP(ctx, sub.URL, ActionRenew, envelope)
	if err != nil {
		return nil, err
	}
	if f := parseSOAPFault(body); f != nil {
		return nil, f
	}
	var env renewEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode renew response: %w", err)
	}
	out := *sub
	out.CurrentTime = parseTimeOrZero(env.Body.Response.CurrentTime)
	out.TerminationTime = parseTimeOrZero(env.Body.Response.TerminationTime)
	return &out, nil
}

// Unsubscribe cancels the subscription. Best-effort: errors are
// returned but the caller should generally proceed with cleanup
// regardless.
func (c *PullPointClient) Unsubscribe(ctx context.Context, sub *Subscription) error {
	envelope := c.buildUnsubscribeEnvelope()
	body, err := c.doSOAP(ctx, sub.URL, ActionUnsubscribe, envelope)
	if err != nil {
		return err
	}
	if f := parseSOAPFault(body); f != nil {
		return f
	}
	return nil
}

func (c *PullPointClient) buildCreateEnvelope(dur time.Duration) []byte {
	header := c.buildAuthHeader()
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:tev="` + NSEvents + `">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body>` +
		`<tev:CreatePullPointSubscription>` +
		`<tev:InitialTerminationTime>PT` + durationSecondsString(dur) + `S</tev:InitialTerminationTime>` +
		`</tev:CreatePullPointSubscription>` +
		`</s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

func (c *PullPointClient) buildPullEnvelope(timeout time.Duration, maxMessages int) []byte {
	header := c.buildAuthHeader()
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:tev="` + NSEvents + `">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body>` +
		`<tev:PullMessages>` +
		`<tev:Timeout>PT` + durationSecondsString(timeout) + `S</tev:Timeout>` +
		`<tev:MessageLimit>` + intString(maxMessages) + `</tev:MessageLimit>` +
		`</tev:PullMessages>` +
		`</s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

func (c *PullPointClient) buildRenewEnvelope(dur time.Duration) []byte {
	header := c.buildAuthHeader()
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:wsnt="` + NSWSNotify + `">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body>` +
		`<wsnt:Renew>` +
		`<wsnt:TerminationTime>PT` + durationSecondsString(dur) + `S</wsnt:TerminationTime>` +
		`</wsnt:Renew>` +
		`</s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

func (c *PullPointClient) buildUnsubscribeEnvelope() []byte {
	header := c.buildAuthHeader()
	tpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="` + NSEnvelope + `"` +
		` xmlns:wsnt="` + NSWSNotify + `">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body><wsnt:Unsubscribe/></s:Body>` +
		`</s:Envelope>`
	return []byte(tpl)
}

func (c *PullPointClient) buildAuthHeader() string {
	if c.Username == "" {
		return ""
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	created := utcTimestamp(now())
	nonce := c.NewNonce
	if nonce == nil {
		nonce = randomNonce
	}
	return usernameTokenHeader(c.Username, c.Password, nonce(), created)
}

func (c *PullPointClient) doSOAP(ctx context.Context, url, action string, envelope []byte) ([]byte, error) {
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Timeout: c.httpTimeout(),
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="`+action+`"`)
	req.Header.Set("SOAPAction", action)

	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap (events can be chatty)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return body, fmt.Errorf("authentication required: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusInternalServerError {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// httpTimeout returns the HTTP client timeout. Long-poll sessions need
// pull-timeout + handshake slack; non-poll calls finish much faster
// but we use the same client config for simplicity.
func (c *PullPointClient) httpTimeout() time.Duration {
	t := c.PullTimeout
	if t <= 0 {
		t = DefaultPullTimeout
	}
	return t + 15*time.Second
}

// createSubscriptionEnvelope is the parsed shape of
// CreatePullPointSubscriptionResponse.
type createSubscriptionEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Fault *struct {
			Code struct {
				Value string `xml:"Value"`
			} `xml:"Code"`
			Reason struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
		} `xml:"Fault"`
		Response struct {
			SubscriptionReference struct {
				Address string `xml:"Address"`
			} `xml:"SubscriptionReference"`
			CurrentTime     string `xml:"CurrentTime"`
			TerminationTime string `xml:"TerminationTime"`
		} `xml:"CreatePullPointSubscriptionResponse"`
	} `xml:"Body"`
}

// renewEnvelope is the parsed shape of RenewResponse.
type renewEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Response struct {
			CurrentTime     string `xml:"CurrentTime"`
			TerminationTime string `xml:"TerminationTime"`
		} `xml:"RenewResponse"`
	} `xml:"Body"`
}

// pullMessagesEnvelope is the parsed shape of PullMessagesResponse.
//
// The wsnt:Message element wraps a tt:Message child whose attributes
// (UtcTime, PropertyOperation) and child SimpleItems we want. Naming
// the inner field `MessageContent` and pulling attributes through Go's
// stdlib xml namespace-aware decoder works directly without manual
// re-parsing.
type pullMessagesEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		Response struct {
			CurrentTime          string                `xml:"CurrentTime"`
			TerminationTime      string                `xml:"TerminationTime"`
			NotificationMessages []notificationMessage `xml:"NotificationMessage"`
		} `xml:"PullMessagesResponse"`
	} `xml:"Body"`
}

type notificationMessage struct {
	Topic     string           `xml:"Topic"`
	Message   wsntMessageOuter `xml:"Message"`
}

// wsntMessageOuter is the wsnt:Message wrapper. ONVIF puts its
// tt:Message inside this. We capture the inner element's attributes
// + simple items via the embedded TTMessage struct.
type wsntMessageOuter struct {
	TTMessage ttMessage `xml:"Message"`
}

// ttMessage models the tt:Message element. xml.Unmarshal handles
// namespace-mismatched elements by matching local names by default, so
// "Message" in any namespace inside wsnt:Message will populate this.
type ttMessage struct {
	UtcTime           string `xml:"UtcTime,attr"`
	PropertyOperation string `xml:"PropertyOperation,attr"`
	Source            struct {
		SimpleItem []ttSimpleItem `xml:"SimpleItem"`
	} `xml:"Source"`
	Data struct {
		SimpleItem []ttSimpleItem `xml:"SimpleItem"`
	} `xml:"Data"`
}

type ttSimpleItem struct {
	Name  string `xml:"Name,attr"`
	Value string `xml:"Value,attr"`
}

// parsePullResponse parses a PullMessagesResponse body and returns the
// list of EventNotifications. Always returns events in the order the
// camera delivered them.
func parsePullResponse(body []byte) (events []EventNotification, currentTime time.Time, terminationTime time.Time, err error) {
	var env pullMessagesEnvelope
	if e := xml.Unmarshal(body, &env); e != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("decode pull response: %w", e)
	}
	currentTime = parseTimeOrZero(env.Body.Response.CurrentTime)
	terminationTime = parseTimeOrZero(env.Body.Response.TerminationTime)
	for _, nm := range env.Body.Response.NotificationMessages {
		ev := EventNotification{
			Topic: strings.TrimSpace(nm.Topic),
			Data:  make(map[string]string),
		}
		mm := nm.Message.TTMessage
		if mm.UtcTime != "" {
			ev.UTCTime = parseTimeOrZero(mm.UtcTime)
		}
		if mm.PropertyOperation != "" {
			ev.PropertyOper = mm.PropertyOperation
		}
		for _, it := range mm.Source.SimpleItem {
			if it.Name != "" {
				ev.Data["source."+it.Name] = it.Value
			}
		}
		for _, it := range mm.Data.SimpleItem {
			if it.Name != "" {
				ev.Data["data."+it.Name] = it.Value
			}
		}
		events = append(events, ev)
	}
	return events, currentTime, terminationTime, nil
}

// parseTimeOrZero parses an ISO-8601 timestamp; on failure returns the
// zero time rather than failing the whole pull.
func parseTimeOrZero(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// durationSecondsString formats a duration in whole seconds for an
// ISO-8601 PT<n>S string.
func durationSecondsString(d time.Duration) string {
	secs := int(d / time.Second)
	if secs <= 0 {
		secs = 1
	}
	return intString(secs)
}

func intString(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
