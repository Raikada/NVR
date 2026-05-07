package notifications

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/outbox"
	"github.com/bluenviron/mediamtx/internal/store"
)

// SubscriptionView is a denormalized join row for matching.
type SubscriptionView struct {
	ID                    string
	TargetID              string
	EventTypeID           string
	CameraID              string
	MinSeverity           string
	QuietHoursStartMinute int
	QuietHoursEndMinute   int
}

// Dispatcher consumes the events bus, matches subscriptions, enqueues
// outbox rows, and runs the outbox processor that delivers them.
type Dispatcher struct {
	store        *store.Store
	vault        *cameracred.Vault
	events       *events.Service
	bus          <-chan events.Event
	unsub        func()
	site         SitePayload
	signURL      func(eventID, kind string) string
	httpClient   *http.Client
	mu           sync.RWMutex
	cache        []SubscriptionView
	cacheStaleAt time.Time
	logger       logger.Writer
}

// NewDispatcher wires a Dispatcher and immediately subscribes to the
// events bus. Call Run to start the bus consumer + outbox processor.
func NewDispatcher(
	st *store.Store,
	vault *cameracred.Vault,
	evs *events.Service,
	site SitePayload,
	signURL func(eventID, kind string) string,
	log logger.Writer,
) *Dispatcher {
	bus, unsub := evs.Subscribe()
	return &Dispatcher{
		store: st, vault: vault, events: evs,
		bus: bus, unsub: unsub,
		site: site, signURL: signURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		logger:     log,
	}
}

// Run blocks until ctx is cancelled. Spins up: (a) the bus consumer
// goroutine that enqueues outbox rows on matching subscriptions; (b)
// the outbox processor that delivers them.
func (d *Dispatcher) Run(ctx context.Context) {
	go d.runBusConsumer(ctx)
	d.runOutbox(ctx)
	d.unsub()
}

func (d *Dispatcher) runBusConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-d.bus:
			if !ok {
				return
			}
			d.handleEvent(ctx, ev)
		}
	}
}

func (d *Dispatcher) handleEvent(ctx context.Context, ev events.Event) {
	subs, err := d.subscriptions(ctx)
	if err != nil {
		if d.logger != nil {
			d.logger.Log(logger.Warn, "[notifications] load subscriptions: %v", err)
		}
		return
	}
	cam, _ := d.store.Cameras.GetByID(ctx, ev.CameraID)
	evType, _ := d.store.EventTypes.GetByID(ctx, ev.TypeID)
	if cam == nil || evType == nil {
		return
	}
	now := time.Now().UTC()
	for _, sub := range subs {
		if !matches(sub, ev, now) {
			continue
		}
		p := Build(uuid.New().String(), d.site, &ev, cam, evType)
		if d.signURL != nil {
			p.SnapshotURL = d.signURL(ev.ID, "full")
			p.ThumbnailURL = d.signURL(ev.ID, "thumb")
		}
		raw, _ := json.Marshal(p)
		row := &store.NotificationOutboxRow{
			ID: p.DeliveryID, TargetID: sub.TargetID, EventID: ev.ID,
			PayloadJSON: string(raw), State: "pending",
			NextAttemptAt: now, CreatedAt: now,
		}
		if err := d.store.NotificationOutbox.Insert(ctx, row); err != nil && d.logger != nil {
			d.logger.Log(logger.Warn, "[notifications] enqueue: %v", err)
		}
	}
}

func (d *Dispatcher) subscriptions(ctx context.Context) ([]SubscriptionView, error) {
	d.mu.RLock()
	if time.Now().Before(d.cacheStaleAt) && d.cache != nil {
		out := d.cache
		d.mu.RUnlock()
		return out, nil
	}
	d.mu.RUnlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.cacheStaleAt) && d.cache != nil {
		return d.cache, nil
	}
	rows, err := d.store.NotificationSubscriptions.ListAllJoined(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionView, len(rows))
	for i, r := range rows {
		out[i] = SubscriptionView{
			ID: r.ID, TargetID: r.TargetID,
			EventTypeID: r.EventTypeID, CameraID: r.CameraID, MinSeverity: r.MinSeverity,
			QuietHoursStartMinute: r.QuietHoursStartMinute,
			QuietHoursEndMinute:   r.QuietHoursEndMinute,
		}
	}
	d.cache = out
	d.cacheStaleAt = time.Now().Add(30 * time.Second)
	return out, nil
}

// InvalidateCache is called by API mutations on notification_targets/subscriptions.
func (d *Dispatcher) InvalidateCache() {
	d.mu.Lock()
	d.cacheStaleAt = time.Time{}
	d.mu.Unlock()
}

var severityRank = map[string]int{"": 0, "info": 1, "warning": 2, "critical": 3}

func matches(sub SubscriptionView, ev events.Event, now time.Time) bool {
	if sub.EventTypeID != "" && sub.EventTypeID != ev.TypeID {
		return false
	}
	if sub.CameraID != "" && sub.CameraID != ev.CameraID {
		return false
	}
	if sub.MinSeverity != "" && severityRank[ev.Severity] < severityRank[sub.MinSeverity] {
		return false
	}
	if sub.QuietHoursStartMinute >= 0 && sub.QuietHoursEndMinute >= 0 {
		minute := now.Hour()*60 + now.Minute()
		if inQuietHours(minute, sub.QuietHoursStartMinute, sub.QuietHoursEndMinute) {
			return false
		}
	}
	return true
}

func inQuietHours(minute, start, end int) bool {
	if end > start {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

func (d *Dispatcher) runOutbox(ctx context.Context) {
	wsender := &WebhookSender{HTTP: d.httpClient, Timeout: 5 * time.Second}
	cfg := outbox.Config{
		Workers:     d.workersFromSettings(ctx),
		MaxAttempts: d.maxAttemptsFromSettings(ctx),
	}
	funcs := outbox.Funcs{
		Claim: func(ctx context.Context, n int) ([]*outbox.Job, error) {
			rows, err := d.store.NotificationOutbox.ClaimBatch(ctx, n)
			if err != nil {
				return nil, err
			}
			out := make([]*outbox.Job, len(rows))
			for i, r := range rows {
				out[i] = &outbox.Job{ID: r.ID, Attempts: r.Attempts, PayloadJSON: r.PayloadJSON, Extra: r.TargetID}
			}
			return out, nil
		},
		Dispatch: func(ctx context.Context, j *outbox.Job) (outbox.Result, string, error) {
			targetID := j.Extra.(string)
			tgt, err := d.store.NotificationTargets.GetByID(ctx, targetID)
			if err != nil || tgt == nil || !tgt.Enabled {
				return outbox.ResultDead, "target missing or disabled", nil
			}
			var p Payload
			if err := json.Unmarshal([]byte(j.PayloadJSON), &p); err != nil {
				return outbox.ResultDead, "payload unmarshal: " + err.Error(), nil
			}
			switch tgt.Kind {
			case "webhook":
				secret, derr := d.vault.Decrypt(tgt.WebhookSecretCiphertext, tgt.WebhookSecretNonce)
				if derr != nil {
					return outbox.ResultDead, "decrypt secret: " + derr.Error(), nil
				}
				status, body, err := wsender.Send(ctx, tgt.WebhookURL, secret, &p)
				if err != nil {
					return outbox.ResultRetry, fmt.Sprintf("transport: %v", err), nil
				}
				if status >= 200 && status < 300 {
					return outbox.ResultDelivered, "", nil
				}
				if status >= 400 && status < 500 {
					return outbox.ResultDead, fmt.Sprintf("HTTP %d: %s", status, body), nil
				}
				return outbox.ResultRetry, fmt.Sprintf("HTTP %d: %s", status, body), nil
			case "email":
				ss, err := d.smtpSettings(ctx)
				if err != nil {
					return outbox.ResultRetry, "smtp settings: " + err.Error(), nil
				}
				subject, html, err := RenderEmail(&p)
				if err != nil {
					return outbox.ResultDead, "render: " + err.Error(), nil
				}
				if err := SendEmail(ctx, ss, tgt.EmailAddress, subject, html); err != nil {
					return outbox.ResultRetry, "smtp send: " + err.Error(), nil
				}
				return outbox.ResultDelivered, "", nil
			default:
				return outbox.ResultDead, "unknown kind: " + tgt.Kind, nil
			}
		},
		MarkDelivered: d.store.NotificationOutbox.MarkDelivered,
		MarkFailed:    d.store.NotificationOutbox.MarkFailed,
		MarkDead:      d.store.NotificationOutbox.MarkDead,
	}
	outbox.NewProcessor(funcs, cfg, d.logger).Run(ctx)
}

func (d *Dispatcher) workersFromSettings(ctx context.Context) int {
	n, _ := d.store.SystemSettings.GetInt(ctx, "outbox_workers", 4)
	return n
}

func (d *Dispatcher) maxAttemptsFromSettings(ctx context.Context) int {
	n, _ := d.store.SystemSettings.GetInt(ctx, "outbox_max_attempts", 8)
	return n
}

func (d *Dispatcher) smtpSettings(ctx context.Context) (SMTPSettings, error) {
	get := func(k string) string {
		s, err := d.store.SystemSettings.Get(ctx, k)
		if err != nil || s == nil {
			return ""
		}
		return s.Value
	}
	port, _ := strconv.Atoi(get("smtp_port"))
	if port == 0 {
		port = 587
	}
	pwCT, _ := hexToBytes(get("smtp_password_ciphertext"))
	pwNonce, _ := hexToBytes(get("smtp_password_nonce"))
	pw := ""
	if len(pwCT) > 0 && len(pwNonce) > 0 {
		decoded, err := d.vault.Decrypt(pwCT, pwNonce)
		if err != nil {
			return SMTPSettings{}, err
		}
		pw = string(decoded)
	}
	useTLS := get("smtp_use_tls") == "true"
	host := get("smtp_host")
	if host == "" {
		return SMTPSettings{}, errors.New("smtp_host not configured")
	}
	return SMTPSettings{
		Host: host, Port: port,
		Username: get("smtp_username"), Password: pw,
		FromAddress: get("smtp_from_address"), UseTLS: useTLS,
	}, nil
}

// hexToBytes decodes a hex-encoded system_settings value (binary stored
// as text). Empty string yields nil (no error).
func hexToBytes(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}
