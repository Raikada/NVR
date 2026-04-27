// Package api: /v1/cameras handlers per ADR 0009 §D5 Cameras.
//
// These handlers replace the old MediaMTX-vocabulary /v3/config/paths/*
// endpoints with the canonical Camera surface. The recorder still
// stores per-camera config in conf.Path internally; the handlers
// translate to/from defs.Camera at the API boundary via Phase 1's
// translators (defs.CameraFromPath / defs.PathFromCamera).
//
// Camera UUIDs are server-issued per ADR 0009 §D4. For paths that
// already exist on disk (which key by name, not UUID), a deterministic
// UUIDv5 is derived from the name so the canonical id is stable across
// recorder restarts.
package api //nolint:revive

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// cameraListResponse is the paginated list shape returned by GET
// /v1/cameras. Mirrors APIPathList structurally but uses the canonical
// Camera shape and JSON snake_case per ADR 0009.
type cameraListResponse struct {
	ItemCount int           `json:"item_count"`
	PageCount int           `json:"page_count"`
	Items     []defs.Camera `json:"items"`
}

// cameraCreateRequest is the body shape for POST /v1/cameras. Mirrors
// defs.Camera but omits server-managed fields (id, runtime, timestamps).
// Decoded as defs.Camera with caller-supplied id/timestamps stripped.
type cameraCreateRequest struct {
	defs.Camera
}

// cameraRuntimeFromAPIPath builds a CameraRuntime from a defs.APIPath
// produced by PathManager. Per ADR 0009 §D3 the runtime block answers
// "is this camera reachable right now?" with online/available booleans
// plus a last_online_at timestamp.
func cameraRuntimeFromAPIPath(p *defs.APIPath) *defs.CameraRuntime {
	rt := &defs.CameraRuntime{
		Online:    p.Online,
		Available: p.Available,
	}
	if p.OnlineTime != nil {
		t := *p.OnlineTime
		rt.LastOnlineAt = &t
	}
	return rt
}

// cameraRuntimeForPath fetches runtime info for a single path-name from
// PathManager. Returns nil when PathManager has no entry for the name
// (camera configured but not yet active in the runtime).
func (a *API) cameraRuntimeForPath(name string) *defs.CameraRuntime {
	if a.PathManager == nil {
		return nil
	}
	p, err := a.PathManager.APIPathsGet(name)
	if err != nil || p == nil {
		return nil
	}
	return cameraRuntimeFromAPIPath(p)
}

// recordingPolicyIDForPath looks up the in-memory RecordingPolicy linkage
// for a conf.Path. Returns nil when no policy has been associated yet
// (lazy-synthesis happens at /v1/recording-policies access).
func recordingPolicyIDForPath(p *conf.Path) *string {
	if p == nil || p.RecordingPolicyID == "" {
		return nil
	}
	id := p.RecordingPolicyID
	return &id
}

// cameraFromConfPath bundles the lookup-and-translate dance used by
// every read handler. Pulls runtime, recording-policy id, and the
// path-name → camera-uuid map together and hands to defs.CameraFromPath.
func (a *API) cameraFromConfPath(c *conf.Conf, p *conf.Path) defs.Camera {
	_, nameToID := cameraIDMaps(c.Paths)
	cameraID := nameToID[p.Name]
	runtime := a.cameraRuntimeForPath(p.Name)
	cam := defs.CameraFromPath(p, cameraID, c.TenantID, recordingPolicyIDForPath(p), nameToID, runtime)
	return cam
}

func (a *API) onV1CamerasList(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	names := make([]string, 0, len(c.Paths))
	for name := range c.Paths {
		names = append(names, name)
	}
	sort.Strings(names)

	out := &cameraListResponse{
		Items: make([]defs.Camera, 0, len(names)),
	}
	for _, name := range names {
		p := c.Paths[name]
		if p == nil {
			continue
		}
		out.Items = append(out.Items, a.cameraFromConfPath(c, p))
	}

	out.ItemCount = len(out.Items)
	pageCount, err := paginate(&out.Items, ctx.Query("items_per_page"), ctx.Query("page"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	out.PageCount = pageCount

	ctx.JSON(http.StatusOK, out)
}

func (a *API) onV1CamerasGet(ctx *gin.Context) {
	id, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	name, ok := pathNameFromCameraID(c.Paths, id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}
	p := c.Paths[name]
	cam := a.cameraFromConfPath(c, p)
	ctx.JSON(http.StatusOK, &cam)
}

// decodeCamera reads a Camera body for POST/PUT/PATCH. Tenant scoping
// is via tenantID on the parsed Camera (not "tenantId" — the canonical
// snake_case key lands on the response, and the request mirrors it).
//
// SourceConfig is decoded as a discriminated payload by SourceType
// (interface fields don't survive json.Unmarshal directly).
func (a *API) decodeCameraBody(ctx *gin.Context) (*defs.Camera, []byte, bool) {
	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return nil, nil, false
	}
	cam, err := unmarshalCamera(body)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return nil, nil, false
	}
	if cam.TenantID != "" && cam.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return nil, nil, false
	}
	return cam, body, true
}

// unmarshalCamera decodes a Camera, special-casing the SourceConfig
// interface field. SourceConfig is parsed by first reading source_type
// to pick the concrete struct type, then unmarshaling the source_config
// raw bytes into it.
func unmarshalCamera(body []byte) (*defs.Camera, error) {
	// First pass: read everything except source_config.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	scRaw, hasSC := raw["source_config"]
	delete(raw, "source_config")

	scrubbed, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}

	var cam defs.Camera
	if err := json.Unmarshal(scrubbed, &cam); err != nil {
		return nil, err
	}

	if hasSC && len(scRaw) > 0 && string(scRaw) != "null" {
		sc, err := unmarshalSourceConfig(cam.SourceType, scRaw)
		if err != nil {
			return nil, err
		}
		cam.SourceConfig = sc
	}
	return &cam, nil
}

// unmarshalSourceConfig picks the concrete SourceConfig type for a
// given Camera SourceType and decodes raw JSON into it. Empty bodies
// produce a zero-valued instance of the appropriate variant.
func unmarshalSourceConfig(st defs.CameraSourceType, raw json.RawMessage) (defs.SourceConfig, error) {
	switch st {
	case defs.CameraSourceTypeRTSP, defs.CameraSourceTypeRTSPS:
		var v defs.SourceConfigRTSP
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypeRTMP, defs.CameraSourceTypeRTMPS:
		var v defs.SourceConfigRTMP
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypeSRT:
		var v defs.SourceConfigSRT
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypeWHEP:
		var v defs.SourceConfigWHEP
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypeRedirect:
		var v defs.SourceConfigRedirect
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypeFile:
		var v defs.SourceConfigFile
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return &v, nil
	case defs.CameraSourceTypePublish:
		return &defs.SourceConfigPublish{}, nil
	case defs.CameraSourceTypeRPiCamera:
		return &defs.SourceConfigRPiCamera{}, nil
	case defs.CameraSourceTypeRTP:
		return &defs.SourceConfigRTP{}, nil
	case defs.CameraSourceTypeHLS:
		return &defs.SourceConfigHLS{}, nil
	}
	// Unknown source_type with non-empty source_config: ignore rather
	// than fail; PathFromCamera will surface a real validation error if
	// the type is genuinely unsupported.
	return nil, nil
}

func (a *API) onV1CamerasPost(ctx *gin.Context) {
	cam, _, ok := a.decodeCameraBody(ctx)
	if !ok {
		return
	}
	if cam.Name == "" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("camera name is required"))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()
	if _, exists := newConf.OptionalPaths[cam.Name]; exists {
		a.writeError(ctx, http.StatusConflict, fmt.Errorf("camera with name '%s' already exists", cam.Name))
		return
	}

	// Server issues the UUID per ADR 0009 §D4. For consistency with the
	// pre-MS deterministic UUIDv5 derivation used elsewhere, we just
	// derive it from the name; the round-trip stays stable regardless
	// of restarts.
	cam.ID = cameraIDFromPathName(cam.Name)
	cam.TenantID = a.Conf.TenantID
	now := time.Now().UTC()
	cam.CreatedAt = now
	cam.UpdatedAt = now
	cam.Runtime = nil

	p, err := defs.PathFromCamera(*cam)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	op, err := optionalPathFromConfPath(p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := newConf.AddPath(cam.Name, op); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	// Stamp the in-memory linkage fields on the freshly-validated path.
	if storedPath, ok := newConf.Paths[cam.Name]; ok {
		storedPath.ID = cam.ID
		if cam.RecordingPolicyID != nil {
			storedPath.RecordingPolicyID = *cam.RecordingPolicyID
		}
	}

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cam.ID,
		Message:     "camera created",
		Attributes: map[string]string{
			"camera_id": cam.ID,
			"verb":      "create",
		},
	})

	cam2 := a.cameraFromConfPath(newConf, newConf.Paths[cam.Name])
	ctx.JSON(http.StatusCreated, &cam2)
}

func (a *API) onV1CamerasPatch(ctx *gin.Context) {
	id, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	body, err := readLimitedBody(ctx)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if !a.checkBodyTenantSnakeCase(ctx, body) {
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	name, ok := pathNameFromCameraID(a.Conf.Paths, id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// Decode into a partial Camera and translate the present fields onto
	// the existing conf.Path.
	patchPtr, err := unmarshalCamera(body)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	patch := *patchPtr

	newConf := a.Conf.Clone()
	existingPath, exists := newConf.Paths[name]
	if !exists {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// Apply the patch onto a clone of existingPath. We let
	// PathFromCamera produce the full mutation, then merge selectively
	// — but for Phase 2 simplicity we treat any present, non-zero field
	// on the patch as an override. The runtime block is read-only and
	// ignored per ADR 0009 §D3.
	merged := mergeCameraOntoConfPath(*existingPath, patch)
	newPath, err := defs.PathFromCamera(merged)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	op, err := optionalPathFromConfPath(newPath)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := newConf.ReplacePath(name, op); err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	if storedPath, ok := newConf.Paths[name]; ok {
		storedPath.ID = id
		if patch.RecordingPolicyID != nil {
			storedPath.RecordingPolicyID = *patch.RecordingPolicyID
		} else if existingPath != nil {
			storedPath.RecordingPolicyID = existingPath.RecordingPolicyID
		}
	}

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   id,
		Message:     "camera patched",
		Attributes: map[string]string{
			"camera_id": id,
			"verb":      "update",
		},
	})

	cam2 := a.cameraFromConfPath(newConf, newConf.Paths[name])
	ctx.JSON(http.StatusOK, &cam2)
}

func (a *API) onV1CamerasPut(ctx *gin.Context) {
	id, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	cam, _, ok := a.decodeCameraBody(ctx)
	if !ok {
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	name, exists := pathNameFromCameraID(a.Conf.Paths, id)
	if !exists {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	// Full replace: overwrite all conf.Path fields. Preserve the
	// recorder-internal name (the route's UUID maps to it).
	cam.ID = id
	cam.Name = name
	cam.TenantID = a.Conf.TenantID
	if cam.CreatedAt.IsZero() {
		// Keep original CreatedAt; fall through with a zero value to be
		// handled below if we lack the original.
	}
	cam.UpdatedAt = time.Now().UTC()
	cam.Runtime = nil

	newConf := a.Conf.Clone()
	newPath, err := defs.PathFromCamera(*cam)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	op, err := optionalPathFromConfPath(newPath)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}
	if err := newConf.ReplacePath(name, op); err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	if storedPath, ok := newConf.Paths[name]; ok {
		storedPath.ID = id
		if cam.RecordingPolicyID != nil {
			storedPath.RecordingPolicyID = *cam.RecordingPolicyID
		}
	}

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   id,
		Message:     "camera replaced",
		Attributes: map[string]string{
			"camera_id": id,
			"verb":      "replace",
		},
	})

	cam2 := a.cameraFromConfPath(newConf, newConf.Paths[name])
	ctx.JSON(http.StatusOK, &cam2)
}

func (a *API) onV1CamerasDelete(ctx *gin.Context) {
	id, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	name, ok := pathNameFromCameraID(a.Conf.Paths, id)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	newConf := a.Conf.Clone()
	if err := newConf.RemovePath(name); err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}
	if err := newConf.Validate(nil); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.publishEventLocked(defs.EventInput{
		Kind:        "config.applied",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   id,
		Message:     "camera deleted",
		Attributes: map[string]string{
			"camera_id": id,
			"verb":      "delete",
		},
	})

	a.writeOK(ctx)
}

// readLimitedBody reads a request body with the API's standard size cap.
func readLimitedBody(ctx *gin.Context) ([]byte, error) {
	r := &customLimitReader{ctx.Request.Body, maxInboundConfigSize}
	buf := bytes.NewBuffer(nil)
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkBodyTenantSnakeCase validates the request body's `tenant_id`
// field (snake_case per ADR 0009) against the recorder's bound tenant.
// Returns true on pass, writes 403 and returns false on mismatch.
//
// readTenantScopedBody handles the camelCase ("tenantId") form for the
// old /v3/* endpoints; the /v1/* endpoints use snake_case throughout.
func (a *API) checkBodyTenantSnakeCase(ctx *gin.Context, body []byte) bool {
	var meta struct {
		TenantID *string `json:"tenant_id"`
	}
	if err := json.Unmarshal(body, &meta); err == nil &&
		meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return false
	}
	return true
}

// optionalPathFromConfPath builds an OptionalPath whose Values include
// every non-zero/non-empty field from the source conf.Path. Used by the
// PATCH/PUT/POST handlers, which run their canonical input through
// defs.PathFromCamera (producing a fully-populated conf.Path) and then
// project that into the OptionalPath shape required by Conf.AddPath /
// ReplacePath.
//
// Round-trips through JSON to leverage the OptionalPath's existing
// reflective decoder; the alternative — direct reflective field copy —
// duplicates the omitempty discipline the JSON tags already encode.
func optionalPathFromConfPath(p *conf.Path) (*conf.OptionalPath, error) {
	byts, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var op conf.OptionalPath
	if err := op.UnmarshalJSON(byts); err != nil {
		return nil, err
	}
	return &op, nil
}

// mergeCameraOntoConfPath produces a defs.Camera that represents
// existing path state with the patch's non-zero/non-nil fields applied
// on top. Pointer fields override only when present; primitive non-zero
// fields override the existing value; primitive zero fields preserve
// the existing value.
//
// The runtime block is unconditionally cleared (writes ignore it per
// ADR 0009 §D3).
func mergeCameraOntoConfPath(existing conf.Path, patch defs.Camera) defs.Camera {
	// Build a Camera from the existing path. We don't have a tenant or
	// nameToID map handy, but PathFromCamera/CameraFromPath round-trip
	// only the fields that matter for serialization back; the few
	// inputs we omit (runtime, recording-policy-id) are reapplied
	// downstream.
	cam := defs.CameraFromPath(&existing, existing.ID, existing.TenantID, nil, nil, nil)

	// Apply patch overrides.
	if patch.SourceType != "" {
		cam.SourceType = patch.SourceType
	}
	if patch.SourceURL != "" {
		cam.SourceURL = patch.SourceURL
	}
	if patch.CredentialsRef != nil {
		cam.CredentialsRef = patch.CredentialsRef
	}
	if patch.SourceConfig != nil {
		cam.SourceConfig = patch.SourceConfig
	}
	if patch.RecordingPolicyID != nil {
		cam.RecordingPolicyID = patch.RecordingPolicyID
	}
	if patch.OnDemand != nil {
		cam.OnDemand = patch.OnDemand
	}
	if patch.MaxReaders != nil {
		cam.MaxReaders = patch.MaxReaders
	}
	if patch.FallbackCameraID != nil {
		cam.FallbackCameraID = patch.FallbackCameraID
	}
	if patch.AlwaysAvailable != nil {
		cam.AlwaysAvailable = patch.AlwaysAvailable
	}
	if patch.SourceFingerprint != nil {
		cam.SourceFingerprint = patch.SourceFingerprint
	}
	if patch.UseAbsoluteTimestamp {
		cam.UseAbsoluteTimestamp = patch.UseAbsoluteTimestamp
	}
	if patch.OverridePublisher {
		cam.OverridePublisher = patch.OverridePublisher
	}
	cam.Runtime = nil
	return cam
}
