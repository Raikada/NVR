// Package api contains the API server.
package api //nolint:revive

import (
	"fmt"
	"net"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
)

const (
	maxInboundConfigSize = 10 * 1024 * 1024
)

func interfaceIsEmpty(i any) bool {
	return reflect.ValueOf(i).Kind() != reflect.Pointer || reflect.ValueOf(i).IsNil()
}

func sortedKeys(paths map[string]*conf.Path) []string {
	ret := make([]string, len(paths))
	i := 0
	for name := range paths {
		ret[i] = name
		i++
	}
	sort.Strings(ret)
	return ret
}

func paramName(ctx *gin.Context) (string, bool) {
	name := ctx.Param("name")

	if len(name) < 2 || name[0] != '/' {
		return "", false
	}

	return name[1:], true
}

type apiAuthManager interface {
	Authenticate(req *auth.Request) (string, *auth.Error)
	AuthenticateWithClaims(req *auth.Request) (string, auth.Claims, *auth.Error)
	RefreshJWTJWKS()
}

type apiParent interface {
	logger.Writer
	APIConfigSet(conf *conf.Conf)
}

// API is an API server.
type API struct {
	Version        string
	Started        time.Time
	Address        string
	DumpPackets    bool
	Encryption     bool
	ServerKey      string
	ServerCert     string
	AllowOrigins   []string
	TrustedProxies conf.IPNetworks
	ReadTimeout    conf.Duration
	WriteTimeout   conf.Duration
	Conf           *conf.Conf
	AuthManager    apiAuthManager
	PathManager    defs.APIPathManager
	RTSPServer     defs.APIRTSPServer
	RTSPSServer    defs.APIRTSPServer
	RTMPServer     defs.APIRTMPServer
	RTMPSServer    defs.APIRTMPServer
	HLSServer      defs.APIHLSServer
	WebRTCServer   defs.APIWebRTCServer
	SRTServer      defs.APISRTServer
	Parent         apiParent

	httpServer   *httpp.Server
	mutex        sync.RWMutex
	networkProbe *networkProbe
}

// Initialize initializes API.
func (a *API) Initialize() error {
	router := gin.New()
	router.SetTrustedProxies(a.TrustedProxies.ToTrustedProxies()) //nolint:errcheck

	router.Use(a.middlewarePreflightRequests)
	router.Use(a.middlewareAuth)

	// The Raikada API roots at /v1 per ADR 0009 §D1. MediaMTX's /v3 lineage
	// is not aliased; there is no deprecation window.
	group := router.Group("/v1")

	group.GET("/info", a.onInfo)

	// Auth endpoint renamed mechanism-neutrally per ADR 0009 §D7.
	// ADR 0011 picked JWT/JWKS for user-facing flows and mTLS X.509
	// for service-to-service connections; the mechanism-neutral name
	// was kept anyway because the endpoint signals "refresh your
	// cached issuer material now" — the JWKS endpoint URL today, but
	// extensible to additional issuer-material kinds (e.g., the trust
	// roots for mTLS validation) without a rename.
	group.POST("/auth/refresh-issuer-material", a.onV1AuthRefreshIssuerMaterial)

	// Cameras (ADR 0009 §D5 Cameras).
	group.GET("/cameras", a.onV1CamerasList)
	group.GET("/cameras/:id", a.onV1CamerasGet)
	group.POST("/cameras", a.onV1CamerasPost)
	group.PATCH("/cameras/:id", a.onV1CamerasPatch)
	group.PUT("/cameras/:id", a.onV1CamerasPut)
	group.DELETE("/cameras/:id", a.onV1CamerasDelete)

	// Recording policies (ADR 0009 §D5 Recording policies).
	group.GET("/recording-policies", a.onV1RecordingPoliciesList)
	group.GET("/recording-policies/:id", a.onV1RecordingPoliciesGet)
	group.POST("/recording-policies", a.onV1RecordingPoliciesPost)
	group.PATCH("/recording-policies/:id", a.onV1RecordingPoliciesPatch)
	group.DELETE("/recording-policies/:id", a.onV1RecordingPoliciesDelete)

	// Streams (ADR 0009 §D5 Streams) — unified runtime-session surface
	// folding 25 protocol-specific endpoints into 3.
	group.GET("/streams", a.onV1StreamsList)
	group.GET("/streams/:id", a.onV1StreamsGet)
	group.DELETE("/streams/:id", a.onV1StreamsDelete)

	// Recordings (ADR 0009 §D5 Recordings).
	group.GET("/recordings", a.onV1RecordingsList)
	group.GET("/recordings/:id", a.onV1RecordingsGet)
	group.GET("/recordings/:id/playback", a.onV1RecordingsPlayback)
	group.DELETE("/recordings/:id", a.onV1RecordingsDelete)

	// Recording segments (ADR 0009 §D5 Recording segments).
	group.GET("/recording-segments", a.onV1RecordingSegmentsList)
	group.GET("/recording-segments/:id", a.onV1RecordingSegmentsGet)
	group.DELETE("/recording-segments/:id", a.onV1RecordingSegmentsDelete)

	// Events (ADR 0009 §D5 Events).
	group.GET("/events", a.onV1EventsList)
	group.GET("/events/:id", a.onV1EventsGet)

	// Audit log (ADR 0006). GET-only — the chain is append-only.
	group.GET("/audit", a.onV1AuditList)

	// Clips (ADR 0009 §D5 follow-up; recorder-authoritative for
	// preparation per ARCHITECTURE.md §5 item 6).
	group.POST("/clips", a.onV1ClipsPost)
	group.GET("/clips", a.onV1ClipsList)
	group.GET("/clips/:id", a.onV1ClipsGet)
	group.DELETE("/clips/:id", a.onV1ClipsDelete)
	group.GET("/clips/:id/download", a.onV1ClipsDownload)

	// Health (ADR 0009 §D5 Health).
	group.GET("/health", a.onV1HealthGet)

	// Storage volumes (ADR 0009 §D5 Storage volumes).
	group.GET("/storage-volumes", a.onV1StorageVolumesList)
	group.GET("/storage-volumes/:id", a.onV1StorageVolumesGet)

	// Recorder-localized escape hatch (ADR 0009 §D6).
	group.GET("/recorder/config", a.onV1RecorderConfigGet)
	group.PATCH("/recorder/config", a.onV1RecorderConfigPatch)
	group.GET("/recorder/camera-defaults", a.onV1RecorderCameraDefaultsGet)
	group.PATCH("/recorder/camera-defaults", a.onV1RecorderCameraDefaultsPatch)
	group.GET("/recorder/cameras/:id/source-config", a.onV1RecorderCameraSourceConfigGet)
	group.PATCH("/recorder/cameras/:id/source-config", a.onV1RecorderCameraSourceConfigPatch)
	group.GET("/recorder/cameras/:id/hooks", a.onV1RecorderCameraHooksGet)
	group.PATCH("/recorder/cameras/:id/hooks", a.onV1RecorderCameraHooksPatch)

	if !interfaceIsEmpty(a.HLSServer) {
		group.GET("/recorder/hls-muxers", a.onV1RecorderHLSMuxersList)
		group.GET("/recorder/hls-muxers/:id", a.onV1RecorderHLSMuxersGet)
		group.GET("/recorder/cameras/:id/snapshot", a.onV1RecorderCameraSnapshot)
	}

	a.httpServer = &httpp.Server{
		Address:           a.Address,
		AllowOrigins:      a.AllowOrigins,
		DumpPackets:       a.DumpPackets,
		DumpPacketsPrefix: "api_server_conn",
		ReadTimeout:       time.Duration(a.ReadTimeout),
		WriteTimeout:      time.Duration(a.WriteTimeout),
		Encryption:        a.Encryption,
		ServerCert:        a.ServerCert,
		ServerKey:         a.ServerKey,
		Handler:           router,
		Parent:            a,
	}
	err := a.httpServer.Initialize()
	if err != nil {
		return err
	}

	str := "listener opened on " + a.Address
	if !a.Encryption {
		str += " (TCP/HTTP)"
	} else {
		str += " (TCP/HTTPS)"
	}
	a.Log(logger.Info, str)

	return nil
}

// Close closes the API.
func (a *API) Close() {
	a.Log(logger.Info, "listener is closing")
	a.httpServer.Close()
}

// Log implements logger.Writer.
func (a *API) Log(level logger.Level, format string, args ...any) {
	if a.Parent == nil {
		return
	}
	a.Parent.Log(level, "[API] "+format, args...)
}

func (a *API) writeError(ctx *gin.Context, status int, err error) {
	// show error in logs
	a.Log(logger.Error, err.Error())

	// add error to response
	ctx.AbortWithStatusJSON(status, &defs.APIError{
		Status: defs.APIErrorStatusError,
		Error:  err.Error(),
	})
}

func (a *API) writeErrorNoLog(ctx *gin.Context, status int, err error) {
	ctx.AbortWithStatusJSON(status, &defs.APIError{
		Status: defs.APIErrorStatusError,
		Error:  err.Error(),
	})
}

func (a *API) writeOK(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, &defs.APIOK{Status: defs.APIOKStatusOK})
}

func (a *API) middlewarePreflightRequests(ctx *gin.Context) {
	if ctx.Request.Method == http.MethodOptions &&
		ctx.Request.Header.Get("Access-Control-Request-Method") != "" {
		ctx.Header("Access-Control-Allow-Methods", "OPTIONS, GET, POST, PATCH, DELETE")
		ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		ctx.AbortWithStatus(http.StatusNoContent)
		return
	}
}

func (a *API) middlewareAuth(ctx *gin.Context) {
	req := &auth.Request{
		Action:      conf.AuthActionAPI,
		Query:       ctx.Request.URL.RawQuery,
		Credentials: httpp.Credentials(ctx.Request),
		IP:          net.ParseIP(ctx.ClientIP()),
	}

	_, claims, err := a.AuthManager.AuthenticateWithClaims(req)
	if err != nil {
		if err.AskCredentials {
			ctx.Header("WWW-Authenticate", `Basic realm="mediamtx"`)
			a.writeErrorNoLog(ctx, http.StatusUnauthorized, fmt.Errorf("authentication error"))
			return
		}

		a.Log(logger.Info, "connection %v failed to authenticate: %v", httpp.RemoteAddr(ctx), err.Wrapped)

		// Publish an auth.failed_login Event alongside the existing log
		// line. Source IP is the only attribute we surface; user
		// identifier is intentionally omitted (a failed-login event
		// must never carry the attempted credential's user field, per
		// data-classification.md / AGENTS.md §9 "never log credentials").
		a.publishEvent(defs.EventInput{
			Kind:        "auth.failed_login",
			Severity:    defs.EventSeverityWarning,
			SubjectKind: defs.EventSubjectKindSession,
			Message:     "API authentication failed",
			Attributes: map[string]string{
				"remote_addr": httpp.RemoteAddr(ctx),
			},
		})
		// Audit chain entry per ADR 0006 D1: every authentication
		// attempt is audited, success and failure both. This is
		// security-focused and lives alongside (not in place of) the
		// auth.failed_login Event. ActorID is omitted: a failed-login
		// audit entry must never carry the attempted credential's user
		// field per data-classification.md cross-cutting rule 8.
		a.emitAuthDecision(
			defs.AuditOutcomeFailure,
			defs.AuditActorKindUnauthenticated,
			"",
			httpp.RemoteAddr(ctx),
			"",
			map[string]string{"reason": "authentication failed"},
		)

		// On the failure path no Principal is set; downstream code is
		// aborted via writeErrorNoLog so it should never read it, but
		// principalFromContext returns the unauthenticated principal
		// defensively if it does.

		// wait some seconds to delay brute force attacks
		<-time.After(auth.PauseAfterError)

		a.writeErrorNoLog(ctx, http.StatusUnauthorized, fmt.Errorf("authentication error"))
		return
	}

	// Build the per-request Principal from the parsed auth claims and
	// stash it on the gin.Context for downstream handlers per ADR
	// 0011's recorder-side validation contract. The pre-OQ10
	// internal/HTTP auth paths produce a zero-value Claims (with
	// Method set) which maps to a service-account principal with
	// empty Scope; the JWT path produces a real ADR 0011 D2 principal.
	principal := principalFromAuthClaims(claims, a.globalPIIReadGrant())
	setPrincipalOnContext(ctx, principal)

	// Successful authentication: emit an audit entry per ADR 0006 D1.
	// Now that ADR 0011 is Accepted, the resolved Principal carries
	// PrincipalKind and (for JWT-authed requests) the user UUID. The
	// pre-OQ10 internal/HTTP auth path produces PrincipalKind =
	// service_account with an empty Sub, preserving the prior
	// behavior; JWT requests now produce the real cloud_user /
	// local_user / service_account actor kind from the token's
	// principal_kind claim. No per-request auth.session_started Event
	// (semantic mismatch — see note below); the audit entry is the
	// security-focused record.
	auditActorKind := principal.PrincipalKind
	if auditActorKind == defs.AuditActorKindUnauthenticated {
		// Defensive: if the auth manager accepted the request but
		// the token's principal_kind was missing or unrecognized,
		// fall back to service_account so the audit entry doesn't
		// claim the request was unauthenticated when it wasn't.
		auditActorKind = defs.AuditActorKindServiceAccount
	}
	a.emitAuthDecision(
		defs.AuditOutcomeSuccess,
		auditActorKind,
		principal.Sub,
		httpp.RemoteAddr(ctx),
		"",
		nil,
	)

	// auth.session_started Event per ADR 0011 §"Consequences": same-jti
	// requests deduplicate to the same logical session, so we emit once
	// on first-seen and skip on subsequent requests bearing the same
	// jti. Pre-ADR-0011 internal/HTTP auth paths produce an empty jti
	// (RawJTI == "") and don't carry session lifecycle — Touch returns
	// false for empty jti so they never emit. See session_tracker.go.
	if jtiSessions.Touch(principal.RawJTI, time.Now()) {
		a.publishEvent(defs.EventInput{
			Kind:        "auth.session_started",
			Severity:    defs.EventSeverityInfo,
			SubjectKind: defs.EventSubjectKindUser,
			SubjectID:   principal.Sub,
			Message:     "authentication session started",
			Attributes: map[string]string{
				"principal_kind":     string(principal.PrincipalKind),
				"jti":                principal.RawJTI,
				"client_fingerprint": principal.ClientFingerprint,
			},
		})
	}
}

func (a *API) onInfo(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, &defs.APIInfo{
		TenantID: a.tenantID(),
		Version:  a.Version,
		Started:  a.Started,
	})
}

// onV1AuthRefreshIssuerMaterial handles POST /v1/auth/refresh-issuer-material.
// Renamed from /v3/auth/jwks/refresh per ADR 0009 §D7 to be mechanism-neutral.
// ADR 0011 picked JWT/JWKS for user flows and mTLS for service-to-service;
// the mechanism-neutral name was retained so additional issuer-material
// kinds (e.g., mTLS trust roots) can flow through this endpoint without a
// rename. Today the body refreshes the JWKS cache.
func (a *API) onV1AuthRefreshIssuerMaterial(ctx *gin.Context) {
	a.AuthManager.RefreshJWTJWKS()
	a.writeOK(ctx)
}

// ReloadConf is called by core.
func (a *API) ReloadConf(conf *conf.Conf) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.Conf = conf
}
