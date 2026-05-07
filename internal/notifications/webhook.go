package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebhookSender posts a Payload to a URL with X-Raikada-Signature.
type WebhookSender struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// Send returns (http_status, body_snippet, error). status==0 means a
// transport error happened (timeout, dns, etc.); the dispatcher treats
// status<200||status>=300 as failure.
func (w *WebhookSender) Send(ctx context.Context, url string, secret []byte, p *Payload) (status int, body string, err error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, "", fmt.Errorf("marshal: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	reqCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Raikada-Signature", sig)
	req.Header.Set("X-Raikada-Delivery", p.DeliveryID)
	req.Header.Set("X-Raikada-Event", p.Event.Type)

	resp, err := w.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	body = string(buf[:n])
	return resp.StatusCode, body, nil
}
