package api //nolint:revive

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/conf/jsonwrapper"
	"github.com/gin-gonic/gin"
)

// RPiCameraSourceConfig is the recorder-localized escape-hatch shape
// for the Raspberry Pi camera ~30-field source configuration. It
// mirrors the RPICamera* fields on conf.Path 1:1; the JSON tags are
// kept identical to conf.Path so PATCH bodies round-trip cleanly
// through copyStructFields.
//
// This is NOT a canonical defs.* type. Per ADR 0009 §D5 (Cameras) and
// the Camera amendment, the canonical Camera.source_config field is a
// generic discriminated union; the RPi sub-shape is explicitly carved
// out into the escape hatch because it is recorder-platform-specific
// hardware integration that the canonical model has no reason to
// reach into.
type RPiCameraSourceConfig struct {
	// TenantID JSON tag is snake_case per the canonical /v1 surface
	// convention; the rpiCamera* fields retain MediaMTX-lineage
	// camelCase per the escape-hatch carve-out (they mirror conf.Path
	// 1:1 for round-trip compatibility with the upstream shape).
	TenantID string `json:"tenant_id,omitempty"`

	RPICameraCamID               uint      `json:"rpiCameraCamID"`
	RPICameraSecondary           bool      `json:"rpiCameraSecondary"`
	RPICameraWidth               uint      `json:"rpiCameraWidth"`
	RPICameraHeight              uint      `json:"rpiCameraHeight"`
	RPICameraHFlip               bool      `json:"rpiCameraHFlip"`
	RPICameraVFlip               bool      `json:"rpiCameraVFlip"`
	RPICameraBrightness          float64   `json:"rpiCameraBrightness"`
	RPICameraContrast            float64   `json:"rpiCameraContrast"`
	RPICameraSaturation          float64   `json:"rpiCameraSaturation"`
	RPICameraSharpness           float64   `json:"rpiCameraSharpness"`
	RPICameraExposure            string    `json:"rpiCameraExposure"`
	RPICameraAWB                 string    `json:"rpiCameraAWB"`
	RPICameraAWBGains            []float64 `json:"rpiCameraAWBGains"`
	RPICameraDenoise             string    `json:"rpiCameraDenoise"`
	RPICameraShutter             uint      `json:"rpiCameraShutter"`
	RPICameraMetering            string    `json:"rpiCameraMetering"`
	RPICameraGain                float64   `json:"rpiCameraGain"`
	RPICameraEV                  float64   `json:"rpiCameraEV"`
	RPICameraROI                 string    `json:"rpiCameraROI"`
	RPICameraHDR                 bool      `json:"rpiCameraHDR"`
	RPICameraTuningFile          string    `json:"rpiCameraTuningFile"`
	RPICameraMode                string    `json:"rpiCameraMode"`
	RPICameraFPS                 float64   `json:"rpiCameraFPS"`
	RPICameraAfMode              string    `json:"rpiCameraAfMode"`
	RPICameraAfRange             string    `json:"rpiCameraAfRange"`
	RPICameraAfSpeed             string    `json:"rpiCameraAfSpeed"`
	RPICameraLensPosition        float64   `json:"rpiCameraLensPosition"`
	RPICameraAfWindow            string    `json:"rpiCameraAfWindow"`
	RPICameraFlickerPeriod       uint      `json:"rpiCameraFlickerPeriod"`
	RPICameraTextOverlayEnable   bool      `json:"rpiCameraTextOverlayEnable"`
	RPICameraTextOverlay         string    `json:"rpiCameraTextOverlay"`
	RPICameraCodec               string    `json:"rpiCameraCodec"`
	RPICameraIDRPeriod           uint      `json:"rpiCameraIDRPeriod"`
	RPICameraBitrate             uint      `json:"rpiCameraBitrate"`
	RPICameraHardwareH264Profile string    `json:"rpiCameraHardwareH264Profile"`
	RPICameraHardwareH264Level   string    `json:"rpiCameraHardwareH264Level"`
	RPICameraSoftwareH264Profile string    `json:"rpiCameraSoftwareH264Profile"`
	RPICameraSoftwareH264Level   string    `json:"rpiCameraSoftwareH264Level"`
	RPICameraMJPEGQuality        uint      `json:"rpiCameraMJPEGQuality"`
}

// rpiCameraSourceConfigFromPath copies the RPi-camera fields out of a
// conf.Path into the wire shape.
func rpiCameraSourceConfigFromPath(p *conf.Path) *RPiCameraSourceConfig {
	return &RPiCameraSourceConfig{
		RPICameraCamID:               p.RPICameraCamID,
		RPICameraSecondary:           p.RPICameraSecondary,
		RPICameraWidth:               p.RPICameraWidth,
		RPICameraHeight:              p.RPICameraHeight,
		RPICameraHFlip:               p.RPICameraHFlip,
		RPICameraVFlip:               p.RPICameraVFlip,
		RPICameraBrightness:          p.RPICameraBrightness,
		RPICameraContrast:            p.RPICameraContrast,
		RPICameraSaturation:          p.RPICameraSaturation,
		RPICameraSharpness:           p.RPICameraSharpness,
		RPICameraExposure:            p.RPICameraExposure,
		RPICameraAWB:                 p.RPICameraAWB,
		RPICameraAWBGains:            p.RPICameraAWBGains,
		RPICameraDenoise:             p.RPICameraDenoise,
		RPICameraShutter:             p.RPICameraShutter,
		RPICameraMetering:            p.RPICameraMetering,
		RPICameraGain:                p.RPICameraGain,
		RPICameraEV:                  p.RPICameraEV,
		RPICameraROI:                 p.RPICameraROI,
		RPICameraHDR:                 p.RPICameraHDR,
		RPICameraTuningFile:          p.RPICameraTuningFile,
		RPICameraMode:                p.RPICameraMode,
		RPICameraFPS:                 p.RPICameraFPS,
		RPICameraAfMode:              p.RPICameraAfMode,
		RPICameraAfRange:             p.RPICameraAfRange,
		RPICameraAfSpeed:             p.RPICameraAfSpeed,
		RPICameraLensPosition:        p.RPICameraLensPosition,
		RPICameraAfWindow:            p.RPICameraAfWindow,
		RPICameraFlickerPeriod:       p.RPICameraFlickerPeriod,
		RPICameraTextOverlayEnable:   p.RPICameraTextOverlayEnable,
		RPICameraTextOverlay:         p.RPICameraTextOverlay,
		RPICameraCodec:               p.RPICameraCodec,
		RPICameraIDRPeriod:           p.RPICameraIDRPeriod,
		RPICameraBitrate:             p.RPICameraBitrate,
		RPICameraHardwareH264Profile: p.RPICameraHardwareH264Profile,
		RPICameraHardwareH264Level:   p.RPICameraHardwareH264Level,
		RPICameraSoftwareH264Profile: p.RPICameraSoftwareH264Profile,
		RPICameraSoftwareH264Level:   p.RPICameraSoftwareH264Level,
		RPICameraMJPEGQuality:        p.RPICameraMJPEGQuality,
	}
}

// onV1RecorderCameraSourceConfigGet serves
// /v1/recorder/cameras/{id}/source-config — the RPi-camera ~30-field
// source-config sub-shape. Recorder-localized escape hatch per
// ADR 0009 §D5/§D6.
//
// Rationale (D6.3): the RPi-camera source-config is hardware-specific
// (white balance, lens position, autofocus mode, ISO/shutter, RPi-
// HW-encoder profiles) and only meaningful on a recorder running on
// Raspberry Pi hardware with the official camera module. The canonical
// Camera.source_config is a generic discriminated union for cross-tier
// portable shapes (RTSP transport, SRT passphrase, WHEP bearer);
// folding 30 RPi-specific fields into it would force every other tier
// to model RPi internals or to special-case `source_type=rpi_camera`
// throughout the canonical surface. The escape hatch contains the
// damage to recorder-local territory.
func (a *API) onV1RecorderCameraSourceConfigGet(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	pathName, ok := pathNameFromCameraID(c.Paths, cameraID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	out := rpiCameraSourceConfigFromPath(c.Paths[pathName])
	out.TenantID = c.TenantID
	ctx.JSON(http.StatusOK, out)
}

// onV1RecorderCameraSourceConfigPatch handles PATCH /v1/recorder/
// cameras/{id}/source-config. The body is decoded as a conf.OptionalPath
// so the existing reflection-based copyStructFields machinery applies
// the patch only to the RPi-camera fields the operator supplies. We
// rely on conf.Validate to reject any combination that would invalidate
// the path.
//
// Rationale (D6.3): see the GET handler.
func (a *API) onV1RecorderCameraSourceConfigPatch(ctx *gin.Context) {
	cameraID, err := validateCameraID(ctx.Param("id"))
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid camera id: %w", err))
		return
	}

	body, ok := a.readTenantSnakeCaseScopedBody(ctx)
	if !ok {
		return
	}

	var p conf.OptionalPath
	err = jsonwrapper.Decode(bytes.NewReader(body), &p)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	pathName, ok := pathNameFromCameraID(a.Conf.Paths, cameraID)
	if !ok {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("camera not found"))
		return
	}

	newConf := a.Conf.Clone()

	err = newConf.PatchPath(pathName, &p)
	if err != nil {
		if errors.Is(err, conf.ErrPathNotFound) {
			a.writeError(ctx, http.StatusNotFound, err)
		} else {
			a.writeError(ctx, http.StatusBadRequest, err)
		}
		return
	}

	err = newConf.Validate(nil)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	a.Parent.APIConfigSet(newConf)

	a.writeOK(ctx)
}
