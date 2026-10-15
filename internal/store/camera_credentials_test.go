package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestCameraCredentials_UpsertAndGet(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("cred-cam")
	mustInsertCamera(t, s, cam)

	exists, err := s.CameraCredentials.Exists(ctx, cam.ID)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Errorf("exists should be false before upsert")
	}

	creds := &CameraCredentials{
		CameraID:                cam.ID,
		Username:                "admin",
		PasswordCiphertext:      []byte{0xde, 0xad, 0xbe, 0xef},
		PasswordNonce:           []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
		OnvifUsername:           "onvif",
		OnvifPasswordCiphertext: []byte{0xca, 0xfe},
		OnvifPasswordNonce:      []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		RotationDueAt:           time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.CameraCredentials.Upsert(ctx, creds); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if creds.RotatedAt.IsZero() {
		t.Errorf("RotatedAt not set on upsert")
	}

	got, err := s.CameraCredentials.Get(ctx, cam.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Username != "admin" || got.OnvifUsername != "onvif" {
		t.Errorf("got %+v", got)
	}
	if !bytes.Equal(got.PasswordCiphertext, creds.PasswordCiphertext) {
		t.Errorf("ct mismatch")
	}
	if !bytes.Equal(got.PasswordNonce, creds.PasswordNonce) {
		t.Errorf("nonce mismatch")
	}
	if !bytes.Equal(got.OnvifPasswordCiphertext, creds.OnvifPasswordCiphertext) {
		t.Errorf("onvif ct mismatch")
	}
	if got.RotationDueAt.IsZero() {
		t.Errorf("rotation due lost")
	}

	exists, _ = s.CameraCredentials.Exists(ctx, cam.ID)
	if !exists {
		t.Errorf("exists should be true after upsert")
	}
}

func TestCameraCredentials_RTSPOnly(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("rtsp-only")
	mustInsertCamera(t, s, cam)

	creds := &CameraCredentials{
		CameraID:           cam.ID,
		Username:           "admin",
		PasswordCiphertext: []byte{1, 2, 3},
		PasswordNonce:      []byte{4, 5, 6},
	}
	if err := s.CameraCredentials.Upsert(ctx, creds); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _ := s.CameraCredentials.Get(ctx, cam.ID)
	if got.OnvifUsername != "" {
		t.Errorf("onvif username: %q", got.OnvifUsername)
	}
	if got.OnvifPasswordCiphertext != nil {
		t.Errorf("onvif ct: %v", got.OnvifPasswordCiphertext)
	}
}

func TestCameraCredentials_UpsertReplaces(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("rotate-cam")
	mustInsertCamera(t, s, cam)

	first := &CameraCredentials{
		CameraID:           cam.ID,
		Username:           "admin",
		PasswordCiphertext: []byte{1},
		PasswordNonce:      []byte{2},
	}
	_ = s.CameraCredentials.Upsert(ctx, first)
	firstRotated := first.RotatedAt

	time.Sleep(2 * time.Millisecond)

	second := &CameraCredentials{
		CameraID:           cam.ID,
		Username:           "newadmin",
		PasswordCiphertext: []byte{99},
		PasswordNonce:      []byte{100},
	}
	if err := s.CameraCredentials.Upsert(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, _ := s.CameraCredentials.Get(ctx, cam.ID)
	if got.Username != "newadmin" {
		t.Errorf("not replaced: %+v", got)
	}
	if !got.RotatedAt.After(firstRotated) {
		t.Errorf("rotated_at didn't advance: first=%v second=%v", firstRotated, got.RotatedAt)
	}
}

func TestCameraCredentials_GetMissing(t *testing.T) {
	s := mustOpenStore(t)
	_, err := s.CameraCredentials.Get(context.Background(), "missing-cam")
	if !errors.Is(err, ErrCameraCredentialsNotFound) {
		t.Errorf("expected ErrCameraCredentialsNotFound; got %v", err)
	}
}

func TestCameraCredentials_DeleteCascadesFromCamera(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()

	cam := newCamera("cascade-cam")
	mustInsertCamera(t, s, cam)
	creds := &CameraCredentials{
		CameraID:           cam.ID,
		Username:           "admin",
		PasswordCiphertext: []byte{1},
		PasswordNonce:      []byte{2},
	}
	_ = s.CameraCredentials.Upsert(ctx, creds)

	if err := s.Cameras.Delete(ctx, cam.ID); err != nil {
		t.Fatalf("delete cam: %v", err)
	}
	exists, err := s.CameraCredentials.Exists(ctx, cam.ID)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists {
		t.Errorf("creds row should have cascaded")
	}
}
