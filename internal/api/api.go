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

	httpServer *httpp.Server
	mutex      sync.RWMutex
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
	// ADR 0002 OQ10 keeps non-JWT credential mechanisms (mTLS,
	// opaque-bearer-with-introspection, short-lived service tokens, hybrid)
	// alive as candidates; the endpoint signals "refresh your cached
	// issuer material now," whatever shape that material takes.
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

	// Recording segments (ADR 0009 §D5 Recording segments).
	group.GET("/recording-segments", a.onV1RecordingSegmentsList)
	group.GET("/recording-segments/:id", a.onV1RecordingSegmentsGet)
	group.DELETE("/recording-segments/:id", a.onV1RecordingSegmentsDelete)

	// Events (ADR 0009 §D5 Events).
	group.GET("/events", a.onV1EventsList)
	group.GET("/events/:id", a.onV1EventsGet)

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

	_, err := a.AuthManager.Authenticate(req)
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

		// wait some seconds to delay brute force attacks
		<-time.After(auth.PauseAfterError)

		a.writeErrorNoLog(ctx, http.StatusUnauthorized, fmt.Errorf("authentication error"))
		return
	}
	// Note: no auth.session_started emit here. The canonical kind implies
	// per-session emission, but the recorder has no AuthSession concept
	// yet (ADR 0002 OQ10), so emitting on every authenticated request
	// would produce per-request events under a per-session kind name —
	// a semantic mismatch. Wired once a session model lands.
}

func (a *API) onInfo(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, &defs.APIInfo{
		TenantID: a.tenantID(),
		Version:  a.Version,
		Started:  a.Started,
	})
}

// onV1AuthRefreshIssuerMaterial handles POST /v1/auth/refresh-issuer-material.
// Renamed from /v3/auth/jwks/refresh per ADR 0009 §D7 to be mechanism-neutral
// — ADR 0002 OQ10 has not yet selected a credential mechanism, so the
// endpoint name does not commit to JWT/JWKS specifically.
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
