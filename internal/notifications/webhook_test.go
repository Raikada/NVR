package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookSender_SignsAnd2xx(t *testing.T) {
	secret := []byte("topsecret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		got := r.Header.Get("X-Raikada-Signature")
		if got != want {
			t.Errorf("sig mismatch: got %s want %s", got, want)
		}
		if r.Header.Get("X-Raikada-Delivery") != "d-1" {
			t.Errorf("delivery header missing")
		}
		if r.Header.Get("X-Raikada-Event") != "motion" {
			t.Errorf("event header missing")
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	ws := &WebhookSender{HTTP: srv.Client(), Timeout: time.Second}
	p := &Payload{
		Schema:     PayloadSchema,
		DeliveryID: "d-1",
		Event:      EventPayload{Type: "motion"},
	}
	status, _, err := ws.Send(context.Background(), srv.URL, secret, p)
	if err != nil || status != 204 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestWebhookSender_5xxReturnsRetryStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	ws := &WebhookSender{HTTP: srv.Client(), Timeout: time.Second}
	p := &Payload{Schema: PayloadSchema, DeliveryID: "d-x"}
	status, _, err := ws.Send(context.Background(), srv.URL, []byte("k"), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 503 {
		t.Errorf("got %d, want 503", status)
	}
}
