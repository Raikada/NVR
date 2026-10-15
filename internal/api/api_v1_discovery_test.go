// Package api: SP2 discovery + adopt + capability endpoints.
package api //nolint:revive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/discovery"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/test"
)

type stubProber struct{ devices []onvif.DiscoveredDevice }

func (s *stubProber) Probe(_ context.Context) ([]onvif.DiscoveredDevice, error) {
	return s.devices, nil
}

func amcrestReport() *onvif.CapabilityReport {
	return &onvif.CapabilityReport{
		Device: onvif.DeviceInformation{
			Manufacturer: "Amcrest", Model: "IP5M-T1277EW-AI",
			FirmwareVersion: "V2.800", SerialNumber: "AMC123",
		},
		Profiles: []onvif.MediaProfile{
			{Token: "main", VideoCodec: "H264", Width: 2960, Height: 1668, HasAudio: true},
		},
		SelectedToken: "main",
		StreamURI:     "rtsp://192.168.1.110:554/cam/realmonitor?channel=1&subtype=0&unicast=true&proto=Onvif",
		SnapshotURI:   "http://192.168.1.110/onvifsnapshot/media_service/snapshot?channel=1&subtype=0",
		HasAudio:      true, HasPTZ: false, HasMotion: true,
		EventsXAddr: "http://192.168.1.110/onvif/event_service",
	}
}

// startDiscoveryAPI wires an API with store + cameras service + a
// discovery service fed by a stub prober + a fake capability probe.
func startDiscoveryAPI(t *testing.T, probeFn func(ctx context.Context, xaddr, user, pass string) (*onvif.CapabilityReport, error)) (*API, *http.Client, *store.Store, *discovery.Service) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recorder.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	vault, err := cameracred.Open(dir)
	require.NoError(t, err)
	svc := cameras.NewService(st, vault, cameras.NewBus(), nil)

	prober := &stubProber{devices: []onvif.DiscoveredDevice{{
		EndpointRef:  "urn:uuid:cam-110",
		XAddr:        "http://192.168.1.110/onvif/device_service",
		Manufacturer: "Amcrest",
		Model:        "IP5M-T1277EW-AI",
	}}}
	disco := discovery.New(prober, svc, nil)
	_, err = disco.ProbeNow(context.Background())
	require.NoError(t, err)

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address:              "localhost:9997",
		ReadTimeout:          conf.Duration(10 * time.Second),
		WriteTimeout:         conf.Duration(10 * time.Second),
		Conf:                 cnf,
		AuthManager:          test.NilAuthManager,
		Store:                st,
		CamerasService:       svc,
		Vault:                vault,
		Discovery:            disco,
		ProbeCapabilitiesFn:  probeFn,
		Parent:               &testParent{},
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)

	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return a, &http.Client{Transport: tr}, st, disco
}

func TestDiscoveryListReturnsCache(t *testing.T) {
	_, hc, _, _ := startDiscoveryAPI(t, nil)

	res, err := hc.Get("http://localhost:9997/v1/discovery/cameras")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out struct {
		Items []discovery.Entry `json:"items"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Len(t, out.Items, 1)
	require.Equal(t, "IP5M-T1277EW-AI", out.Items[0].Model)
}

func TestAdoptHappyPath(t *testing.T) {
	_, hc, st, _ := startDiscoveryAPI(t,
		func(_ context.Context, xaddr, user, pass string) (*onvif.CapabilityReport, error) {
			if user != "admin" || pass != "pw" {
				return nil, onvif.ErrUnauthorized
			}
			require.Equal(t, "http://192.168.1.110/onvif/device_service", xaddr)
			return amcrestReport(), nil
		})

	body := `{"endpoint_reference":"urn:uuid:cam-110","name":"front_door","rtsp_username":"admin","rtsp_password":"pw"}`
	res, err := hc.Post("http://localhost:9997/v1/discovery/adopt", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	var out struct {
		Camera struct {
			ID        string `json:"id"`
			SourceURL string `json:"source_url"`
		} `json:"camera"`
		Capabilities struct {
			SelectedProfileToken string `json:"selected_profile_token"`
		} `json:"capabilities"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.NotEmpty(t, out.Camera.ID)
	require.Equal(t,
		"rtsp://192.168.1.110:554/cam/realmonitor?channel=1&subtype=0&unicast=true&proto=Onvif",
		out.Camera.SourceURL)

	// Store row exists with vendor identity + xaddr.
	row, err := st.Cameras.GetByID(context.Background(), out.Camera.ID)
	require.NoError(t, err)
	require.Equal(t, "front_door", row.Name)
	require.Equal(t, "Amcrest", row.Manufacturer)
	require.Equal(t, "http://192.168.1.110/onvif/device_service", row.OnvifXAddr)

	// Credentials in the vault.
	creds, err := st.CameraCredentials.Get(context.Background(), out.Camera.ID)
	require.NoError(t, err)
	require.Equal(t, "admin", creds.Username)

	// Capabilities row persisted.
	caps, err := st.CameraCapabilities.Get(context.Background(), out.Camera.ID)
	require.NoError(t, err)
	require.Equal(t, "main", caps.SelectedProfileToken)
	require.True(t, caps.HasAudio)
	require.True(t, caps.HasMotion)
}

func TestAdoptBadCredentialsReturns400(t *testing.T) {
	_, hc, st, _ := startDiscoveryAPI(t,
		func(_ context.Context, _, _, _ string) (*onvif.CapabilityReport, error) {
			return nil, fmt.Errorf("device information: %w", onvif.ErrUnauthorized)
		})

	body := `{"endpoint_reference":"urn:uuid:cam-110","name":"front_door","rtsp_username":"admin","rtsp_password":"wrong"}`
	res, err := hc.Post("http://localhost:9997/v1/discovery/adopt", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	rows, err := st.Cameras.List(context.Background(), store.ListCamerasFilter{})
	require.NoError(t, err)
	require.Empty(t, rows, "failed adopt must create nothing")
}

func TestAdoptUnknownRefReturns404(t *testing.T) {
	_, hc, _, _ := startDiscoveryAPI(t, nil)
	body := `{"endpoint_reference":"urn:uuid:ghost","name":"x","rtsp_username":"a","rtsp_password":"b"}`
	res, err := hc.Post("http://localhost:9997/v1/discovery/adopt", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestAdoptUnreachableCameraReturns502(t *testing.T) {
	_, hc, _, _ := startDiscoveryAPI(t,
		func(_ context.Context, _, _, _ string) (*onvif.CapabilityReport, error) {
			return nil, errors.New("post: dial tcp: i/o timeout")
		})
	body := `{"endpoint_reference":"urn:uuid:cam-110","name":"x","rtsp_username":"a","rtsp_password":"b"}`
	res, err := hc.Post("http://localhost:9997/v1/discovery/adopt", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusBadGateway, res.StatusCode)
}

func TestCameraCapabilitiesGet(t *testing.T) {
	_, hc, st, _ := startDiscoveryAPI(t, nil)

	require.NoError(t, st.Cameras.Insert(context.Background(), &store.Camera{
		ID: "cam-caps", Name: "caps", SourceType: "rtsp",
		SourceURL: "rtsp://192.0.2.1/s", Enabled: true,
	}))
	require.NoError(t, st.CameraCapabilities.Upsert(context.Background(), &store.CameraCapabilities{
		CameraID:             "cam-caps",
		ProfilesJSON:         `[{"token":"main","width":2960,"height":1668}]`,
		SelectedProfileToken: "main",
		HasAudio:             true,
		ProbedAt:             time.Now().UTC(),
	}))

	res, err := hc.Get("http://localhost:9997/v1/cameras/cam-caps/capabilities")
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var out struct {
		SelectedProfileToken string          `json:"selected_profile_token"`
		HasAudio             bool            `json:"has_audio"`
		Profiles             json.RawMessage `json:"profiles"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, "main", out.SelectedProfileToken)
	require.True(t, out.HasAudio)
	require.Contains(t, string(out.Profiles), "2960")
}

func TestCameraReProbeRefreshesCapabilities(t *testing.T) {
	_, hc, st, _ := startDiscoveryAPI(t,
		func(_ context.Context, xaddr, user, pass string) (*onvif.CapabilityReport, error) {
			require.Equal(t, "http://192.168.1.110/onvif/device_service", xaddr)
			require.Equal(t, "admin", user)
			require.Equal(t, "pw", pass)
			return amcrestReport(), nil
		})

	// Adopt first (gives us camera + vault credentials + xaddr).
	body := `{"endpoint_reference":"urn:uuid:cam-110","name":"front_door","rtsp_username":"admin","rtsp_password":"pw"}`
	res, err := hc.Post("http://localhost:9997/v1/discovery/adopt", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	rows, err := st.Cameras.List(context.Background(), store.ListCamerasFilter{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	id := rows[0].ID

	// Wipe the capabilities row, then re-probe restores it.
	require.NoError(t, st.CameraCapabilities.Delete(context.Background(), id))
	res2, err := hc.Post("http://localhost:9997/v1/cameras/"+id+"/probe", "application/json", nil)
	require.NoError(t, err)
	defer res2.Body.Close()
	require.Equal(t, http.StatusOK, res2.StatusCode)

	caps, err := st.CameraCapabilities.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "main", caps.SelectedProfileToken)
}
