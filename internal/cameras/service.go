package cameras

import (
	"context"
	"errors"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/store"
)

// OnvifTeardown is the small seam this package uses to ask the ONVIF
// manager to drop subscriptions for a deleted camera. Phase 6 supplies
// the concrete adapter; tests pass nil.
type OnvifTeardown interface {
	TeardownCamera(ctx context.Context, cameraID string) error
}

// Service is the canonical entry point for camera CRUD. Calls the right
// repos in the right order, materializes URLs via the credential vault
// when needed, and publishes a Change on the bus per mutation.
type Service struct {
	store *store.Store
	vault *cameracred.Vault
	bus   *Bus
	onvif OnvifTeardown
}

// NewService wires a Service. onvif may be nil (tests, or before Phase 6).
func NewService(st *store.Store, vault *cameracred.Vault, bus *Bus, onvif OnvifTeardown) *Service {
	return &Service{store: st, vault: vault, bus: bus, onvif: onvif}
}

// Insert persists the camera, optionally writes credentials (when
// plaintextUsername is non-empty), and publishes ChangeCreated.
func (s *Service) Insert(ctx context.Context, cam *store.Camera, plaintextUsername, plaintextPassword string) error {
	if err := s.store.Cameras.Insert(ctx, cam); err != nil {
		return err
	}
	if plaintextUsername != "" {
		if err := s.upsertCredentials(ctx, cam.ID, plaintextUsername, plaintextPassword); err != nil {
			// Roll back the camera insert so the caller sees a clean failure.
			_ = s.store.Cameras.Delete(ctx, cam.ID)
			return err
		}
	}
	s.bus.Publish(Change{Type: ChangeCreated, Camera: cam, CameraID: cam.ID})
	return nil
}

// Get returns the camera row.
func (s *Service) Get(ctx context.Context, id string) (*store.Camera, error) {
	return s.store.Cameras.GetByID(ctx, id)
}

// List enumerates cameras matching the filter.
func (s *Service) List(ctx context.Context, f store.ListCamerasFilter) ([]*store.Camera, error) {
	return s.store.Cameras.List(ctx, f)
}

// Update writes the row and publishes ChangeUpdated.
func (s *Service) Update(ctx context.Context, cam *store.Camera) error {
	if err := s.store.Cameras.Update(ctx, cam); err != nil {
		return err
	}
	s.bus.Publish(Change{Type: ChangeUpdated, Camera: cam, CameraID: cam.ID})
	return nil
}

// SetCredentials encrypts + stores the camera's RTSP credentials and
// publishes a ChangeUpdated so the path manager can re-materialize the
// URL with the new password.
func (s *Service) SetCredentials(ctx context.Context, cameraID, plaintextUsername, plaintextPassword string) error {
	if plaintextUsername == "" {
		return errors.New("cameras: empty username")
	}
	if err := s.upsertCredentials(ctx, cameraID, plaintextUsername, plaintextPassword); err != nil {
		return err
	}
	cam, err := s.store.Cameras.GetByID(ctx, cameraID)
	if err != nil {
		return err
	}
	s.bus.Publish(Change{Type: ChangeUpdated, Camera: cam, CameraID: cameraID})
	return nil
}

func (s *Service) upsertCredentials(ctx context.Context, cameraID, username, password string) error {
	ct, nonce, err := s.vault.Encrypt([]byte(password))
	if err != nil {
		return err
	}
	return s.store.CameraCredentials.Upsert(ctx, &store.CameraCredentials{
		CameraID:           cameraID,
		Username:           username,
		PasswordCiphertext: ct,
		PasswordNonce:      nonce,
	})
}

// Delete tears down ONVIF subscriptions, removes the row (cascades
// take credentials/health/events), and publishes ChangeDeleted.
func (s *Service) Delete(ctx context.Context, id string) error {
	if s.onvif != nil {
		// Best-effort; if onvif teardown fails, we still proceed to
		// remove the camera row — the operator's intent is to delete.
		_ = s.onvif.TeardownCamera(ctx, id)
	}
	if err := s.store.Cameras.Delete(ctx, id); err != nil {
		return err
	}
	s.bus.Publish(Change{Type: ChangeDeleted, CameraID: id})
	return nil
}

// MaterializeRTSPURL returns the camera's source URL with credentials
// (if any) inlined. The result is the only place plaintext password
// meets a string. Callers must NEVER log this value.
func (s *Service) MaterializeRTSPURL(ctx context.Context, cameraID string) (string, error) {
	cam, err := s.store.Cameras.GetByID(ctx, cameraID)
	if err != nil {
		return "", err
	}
	creds, err := s.store.CameraCredentials.Get(ctx, cameraID)
	if errors.Is(err, store.ErrCameraCredentialsNotFound) {
		return cam.SourceURL, nil
	}
	if err != nil {
		return "", err
	}
	plain, err := s.vault.Decrypt(creds.PasswordCiphertext, creds.PasswordNonce)
	if err != nil {
		return "", err
	}
	return cameracred.MaterializeRTSPURL(cam.SourceURL, creds.Username, string(plain)), nil
}

// PlaintextCredentials decrypts and returns the camera's stored RTSP
// credentials for subsystems that must authenticate to the camera
// directly (capability re-probe, vendor event channels, snapshot
// fetch). Callers must NEVER log the password. Returns
// store.ErrCameraCredentialsNotFound when no credentials are stored.
func (s *Service) PlaintextCredentials(ctx context.Context, cameraID string) (string, string, error) {
	creds, err := s.store.CameraCredentials.Get(ctx, cameraID)
	if err != nil {
		return "", "", err
	}
	plain, err := s.vault.Decrypt(creds.PasswordCiphertext, creds.PasswordNonce)
	if err != nil {
		return "", "", err
	}
	return creds.Username, string(plain), nil
}

// Health returns the most recent observed health for the camera. Returns
// nil with no error when no health row has been recorded yet.
func (s *Service) Health(ctx context.Context, cameraID string) (*HealthSnapshot, error) {
	row, err := s.store.CameraHealth.Get(ctx, cameraID)
	if errors.Is(err, store.ErrCameraHealthNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &HealthSnapshot{
		CameraID:            row.CameraID,
		RTSPState:           row.RTSPState,
		LastKeyframeAt:      row.LastKeyframeAt,
		LastEventAt:         row.LastEventAt,
		LastSeenAt:          row.LastSeenAt,
		ConsecutiveFailures: row.ConsecutiveFailures,
		LastError:           row.LastError,
		UpdatedAt:           row.UpdatedAt,
	}, nil
}

// Subscribe is a thin pass-through to Bus.Subscribe so callers can
// avoid importing the bus type directly.
func (s *Service) Subscribe() (<-chan Change, func()) {
	return s.bus.Subscribe()
}
