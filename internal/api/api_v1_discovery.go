// Package api: SP2 discovery + adopt + capability endpoints.
//
//	GET  /v1/discovery/cameras          (camera.list)   cache snapshot
//	POST /v1/discovery/probe            (camera.create) force a probe round
//	POST /v1/discovery/adopt            (camera.create) probe + create + vault + capabilities
//	GET  /v1/cameras/:id/capabilities   (camera.read)   stored capability report
//
// The 501 probe stub in api_v1_camera_extensions.go is also replaced
// here (onV1CameraProbe): re-runs the capability probe with vault
// credentials and refreshes camera_capabilities.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/rbac"
	"github.com/bluenviron/mediamtx/internal/store"
)

func (a *API) registerV1Discovery(r gin.IRouter) {
	r.GET("/discovery/cameras", rbac.RequirePerm(rbac.PermCameraList, a.auditEmitter()), a.onV1DiscoveryList)
	r.POST("/discovery/probe", rbac.RequirePerm(rbac.PermCameraCreate, a.auditEmitter()), a.onV1DiscoveryProbe)
	r.POST("/discovery/adopt", rbac.RequirePerm(rbac.PermCameraCreate, a.auditEmitter()), a.onV1DiscoveryAdopt)
	r.GET("/cameras/:id/capabilities", rbac.RequirePerm(rbac.PermCameraRead, a.auditEmitter()), a.onV1CameraCapabilitiesGet)
}

// probeCapabilities resolves the injected fake or the real prober.
func (a *API) probeCapabilities(ctx context.Context, xaddr, username, password string) (*onvif.CapabilityReport, error) {
	if a.ProbeCapabilitiesFn != nil {
		return a.ProbeCapabilitiesFn(ctx, xaddr, username, password)
	}
	return onvif.ProbeCapabilities(ctx, xaddr, username, password)
}

func (a *API) onV1DiscoveryList(ctx *gin.Context) {
	if a.Discovery == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("discovery not wired"))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": a.Discovery.Snapshot()})
}

func (a *API) onV1DiscoveryProbe(ctx *gin.Context) {
	if a.Discovery == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("discovery not wired"))
		return
	}
	entries, err := a.Discovery.ProbeNow(ctx.Request.Context())
	if err != nil {
		a.writeError(ctx, http.StatusBadGateway, fmt.Errorf("probe: %w", err))
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"items": entries})
}

type discoveryAdoptRequest struct {
	EndpointReference string `json:"endpoint_reference"`
	Name              string `json:"name"`
	RTSPUsername      string `json:"rtsp_username"`
	RTSPPassword      string `json:"rtsp_password"`
}

func (a *API) onV1DiscoveryAdopt(ctx *gin.Context) {
	if !a.guardAdminAction(ctx) {
		return
	}
	if a.Discovery == nil || a.CamerasService == nil || a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("discovery/cameras stack not wired"))
		return
	}
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	var req discoveryAdoptRequest
	if err := json.Unmarshal(body, &req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if req.EndpointReference == "" || req.Name == "" || req.RTSPUsername == "" {
		a.writeError(ctx, http.StatusBadRequest,
			errors.New("endpoint_reference, name and rtsp_username are required"))
		return
	}

	entry, ok := a.Discovery.Lookup(req.EndpointReference)
	if !ok {
		a.writeError(ctx, http.StatusNotFound,
			errors.New("discovered camera not found (entry may have aged out; re-probe)"))
		return
	}

	report, err := a.probeCapabilities(ctx.Request.Context(), entry.XAddr, req.RTSPUsername, req.RTSPPassword)
	if err != nil {
		if errors.Is(err, onvif.ErrUnauthorized) {
			a.writeError(ctx, http.StatusBadRequest,
				fmt.Errorf("camera rejected credentials: %w", err))
			return
		}
		a.writeError(ctx, http.StatusBadGateway, fmt.Errorf("capability probe: %w", err))
		return
	}

	created, ok := a.createCameraCommon(ctx, &defs.Camera{
		Name:       req.Name,
		SourceType: defs.CameraSourceTypeRTSP,
		SourceURL:  report.StreamURI,
	})
	if !ok {
		return
	}

	if err := a.CamerasService.SetCredentials(ctx.Request.Context(), created.ID, req.RTSPUsername, req.RTSPPassword); err != nil {
		a.rollbackAdoptedCamera(ctx, created.ID, created.Name)
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("store credentials: %w", err))
		return
	}

	capsRow, err := capabilitiesRowFromReport(created.ID, entry.XAddr, report, time.Now().UTC())
	if err == nil {
		err = a.Store.CameraCapabilities.Upsert(ctx.Request.Context(), capsRow)
	}
	if err != nil {
		a.rollbackAdoptedCamera(ctx, created.ID, created.Name)
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("store capabilities: %w", err))
		return
	}

	// Enrich the store row with the probed vendor identity + xaddr.
	if row, gerr := a.CamerasService.Get(ctx.Request.Context(), created.ID); gerr == nil {
		row.Manufacturer = report.Device.Manufacturer
		row.Model = report.Device.Model
		row.FirmwareVersion = report.Device.FirmwareVersion
		row.SerialNumber = report.Device.SerialNumber
		row.OnvifXAddr = entry.XAddr
		if err := a.CamerasService.Update(ctx.Request.Context(), row); err != nil {
			a.rollbackAdoptedCamera(ctx, created.ID, created.Name)
			a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("store vendor identity: %w", err))
			return
		}
	}

	a.emitMutationAudit(ctx, "camera.adopted", "camera", created.ID, map[string]string{
		"camera_id": created.ID,
		"model":     report.Device.Model,
	})

	ctx.JSON(http.StatusCreated, gin.H{
		"camera":       created,
		"capabilities": capabilitiesWireFromRow(capsRow),
	})
}

// rollbackAdoptedCamera unwinds a partially-adopted camera: store row
// (cascades credentials/capabilities) and the conf path.
func (a *API) rollbackAdoptedCamera(ctx *gin.Context, id, name string) {
	_ = a.syncCameraStoreDelete(ctx.Request.Context(), id)

	a.mutex.Lock()
	defer a.mutex.Unlock()
	newConf := a.Conf.Clone()
	if err := newConf.RemovePath(name); err == nil {
		if err := newConf.Validate(nil); err == nil {
			a.Conf = newConf
			a.Parent.APIConfigSet(newConf)
		}
	}
}

// onV1CameraProbe re-runs the capability probe for a managed camera
// using its vault credentials + stored ONVIF XAddr, refreshing the
// camera_capabilities row. Replaces the foundation 501 stub.
func (a *API) onV1CameraProbe(ctx *gin.Context) {
	if a.CamerasService == nil || a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("cameras stack not wired"))
		return
	}
	id := ctx.Param("id")
	cam, err := a.CamerasService.Get(ctx.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrCameraNotFound) {
		a.writeError(ctx, http.StatusNotFound, errors.New("camera not found"))
		return
	}
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	if cam.OnvifXAddr == "" {
		a.writeError(ctx, http.StatusBadRequest,
			errors.New("camera has no onvif_xaddr; set one via PATCH before probing"))
		return
	}
	username, password, err := a.CamerasService.PlaintextCredentials(ctx.Request.Context(), id)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest,
			fmt.Errorf("camera has no stored credentials: %w", err))
		return
	}

	report, err := a.probeCapabilities(ctx.Request.Context(), cam.OnvifXAddr, username, password)
	if err != nil {
		if errors.Is(err, onvif.ErrUnauthorized) {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("camera rejected credentials: %w", err))
			return
		}
		a.writeError(ctx, http.StatusBadGateway, fmt.Errorf("capability probe: %w", err))
		return
	}

	capsRow, err := capabilitiesRowFromReport(id, cam.OnvifXAddr, report, time.Now().UTC())
	if err == nil {
		err = a.Store.CameraCapabilities.Upsert(ctx.Request.Context(), capsRow)
	}
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("store capabilities: %w", err))
		return
	}

	a.emitMutationAudit(ctx, "camera.probed", "camera", id, nil)
	ctx.JSON(http.StatusOK, capabilitiesWireFromRow(capsRow))
}

func (a *API) onV1CameraCapabilitiesGet(ctx *gin.Context) {
	if a.Store == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, errors.New("store not wired"))
		return
	}
	row, err := a.Store.CameraCapabilities.Get(ctx.Request.Context(), ctx.Param("id"))
	if errors.Is(err, sql.ErrNoRows) || (err != nil && strings.Contains(err.Error(), "no rows")) {
		a.writeError(ctx, http.StatusNotFound, errors.New("no capability report; probe the camera first"))
		return
	}
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}
	ctx.JSON(http.StatusOK, capabilitiesWireFromRow(row))
}

// vendorCapabilities is the VendorCapabilitiesJSON shape: everything
// probed that has no dedicated column. SP4 reads snapshot_uri from
// here.
type vendorCapabilities struct {
	Device      onvif.DeviceInformation `json:"device"`
	SnapshotURI string                  `json:"snapshot_uri,omitempty"`
	EventsXAddr string                  `json:"events_xaddr,omitempty"`
	OnvifXAddr  string                  `json:"onvif_xaddr"`
}

func capabilitiesRowFromReport(cameraID, xaddr string, r *onvif.CapabilityReport, now time.Time) (*store.CameraCapabilities, error) {
	profiles, err := json.Marshal(r.Profiles)
	if err != nil {
		return nil, err
	}
	vendor, err := json.Marshal(vendorCapabilities{
		Device:      r.Device,
		SnapshotURI: r.SnapshotURI,
		EventsXAddr: r.EventsXAddr,
		OnvifXAddr:  xaddr,
	})
	if err != nil {
		return nil, err
	}
	return &store.CameraCapabilities{
		CameraID:               cameraID,
		ProfilesJSON:           string(profiles),
		SelectedProfileToken:   r.SelectedToken,
		HasAudio:               r.HasAudio,
		HasPTZ:                 r.HasPTZ,
		HasMotion:              r.HasMotion,
		HasIO:                  r.HasIO,
		HasImaging:             r.HasImaging,
		VendorCapabilitiesJSON: string(vendor),
		ProbedAt:               now,
	}, nil
}

func capabilitiesWireFromRow(row *store.CameraCapabilities) gin.H {
	return gin.H{
		"camera_id":              row.CameraID,
		"profiles":               json.RawMessage(row.ProfilesJSON),
		"selected_profile_token": row.SelectedProfileToken,
		"has_audio":              row.HasAudio,
		"has_ptz":                row.HasPTZ,
		"has_motion":             row.HasMotion,
		"has_io":                 row.HasIO,
		"has_imaging":            row.HasImaging,
		"vendor":                 json.RawMessage(orNullJSON(row.VendorCapabilitiesJSON)),
		"probed_at":              row.ProbedAt,
	}
}

// orNullJSON keeps empty stored JSON valid on the wire.
func orNullJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "null"
	}
	return s
}
