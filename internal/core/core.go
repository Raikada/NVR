// Package core contains the main struct of the software.
package core

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/api"
	"github.com/bluenviron/mediamtx/internal/audit"
	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/bootstrap"
	"github.com/bluenviron/mediamtx/internal/cameracred"
	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/cloudbridge"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/confwatcher"
	"github.com/bluenviron/mediamtx/internal/events"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/localauth"
	"github.com/bluenviron/mediamtx/internal/mdns"
	"github.com/bluenviron/mediamtx/internal/motion"
	"github.com/bluenviron/mediamtx/internal/notifications"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/recordingmeta"
	"github.com/bluenviron/mediamtx/internal/retention"
	"github.com/bluenviron/mediamtx/internal/schedule"
	"github.com/bluenviron/mediamtx/internal/softwareupdate"
	recstore "github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/metrics"
	"github.com/bluenviron/mediamtx/internal/playback"
	"github.com/bluenviron/mediamtx/internal/pprof"
	"github.com/bluenviron/mediamtx/internal/recordcleaner"
	"github.com/bluenviron/mediamtx/internal/recorder"
	"github.com/bluenviron/mediamtx/internal/rlimit"
	"github.com/bluenviron/mediamtx/internal/servers/hls"
	"github.com/bluenviron/mediamtx/internal/servers/rtmp"
	"github.com/bluenviron/mediamtx/internal/servers/rtsp"
	"github.com/bluenviron/mediamtx/internal/servers/srt"
	"github.com/bluenviron/mediamtx/internal/servers/webrtc"
)

//go:generate go run ./versiongetter

//go:embed VERSION
var version []byte

var started = time.Now()

var defaultConfPaths = []string{
	"rtsp-simple-server.yml",
	"mediamtx.yml",
}

var defaultConfPathsNotWin = []string{
	"/usr/local/etc/mediamtx.yml",
	"/usr/etc/mediamtx.yml",
	"/etc/mediamtx/mediamtx.yml",
}

func goArm() string {
	bi, _ := debug.ReadBuildInfo()
	for _, bs := range bi.Settings {
		if bs.Key == "GOARM" {
			return bs.Value
		}
	}
	return ""
}

func getArch() string {
	var arch string
	if runtime.GOARCH == "arm" {
		arch = "armv" + goArm()
	} else {
		arch = runtime.GOARCH
	}
	return arch
}

func atLeastOneRecordDeleteAfter(pathConfs map[string]*conf.Path) bool {
	for _, e := range pathConfs {
		if e.RecordDeleteAfter != 0 {
			return true
		}
	}
	return false
}

func getRTPMaxPayloadSize(udpMaxPayloadSize int, rtspEncryption conf.Encryption) int {
	// UDP max payload size - 12 (RTP header)
	v := udpMaxPayloadSize - 12

	// 10 (SRTP HMAC SHA1 authentication tag)
	if rtspEncryption == conf.EncryptionOptional || rtspEncryption == conf.EncryptionStrict {
		v -= 10
	}

	return v
}

var cli struct {
	Confpath string `arg:"" default:""`
	Version  bool   `help:"print version"`
	Upgrade  bool   `help:"upgrade executable to the latest version"`
}

// Core is an instance of MediaMTX.
type Core struct {
	ctx             context.Context
	ctxCancel       func()
	confPath        string
	conf            *conf.Conf
	logger          *logger.Logger
	externalCmdPool *externalcmd.Pool
	authManager     *auth.Manager
	metrics         *metrics.Metrics
	pprof           *pprof.PPROF
	recordCleaner   *recordcleaner.Cleaner
	playbackServer  *playback.Server
	pathManager     *pathManager
	rtspServer      *rtsp.Server
	rtspsServer     *rtsp.Server
	rtmpServer      *rtmp.Server
	rtmpsServer     *rtmp.Server
	hlsServer       *hls.Server
	webRTCServer    *webrtc.Server
	srtServer       *srt.Server
	api             *api.API
	confWatcher     *confwatcher.ConfWatcher
	identity         *identity.Identity
	localAuth        *localauth.Manager
	localAuthStore   *recstore.Store
	mdnsService      *mdns.Service
	onvifManager     *onvif.Manager
	motionController *motion.Controller

	// Phase 6 foundation services. Constructed once at first
	// createResources call; the long-lived goroutines run for the
	// life of Core. Cancelled at shutdown via fndCtxCancel.
	camerasBus      *cameras.Bus
	camerasService  *cameras.Service
	eventsBus       *events.Bus
	eventsService   *events.Service
	scheduleResolver *schedule.Resolver
	notifDispatcher *notifications.Dispatcher
	retentionMgr    *retention.Manager
	cloudSvc        *cloudbridge.Service
	pathBridge      *cameras.PathBridge
	pmAdapter       *pathManagerAdapter
	credVault       *cameracred.Vault
	auditEmit       *audit.Emitter
	// tlsReloader watches tls.crt + tls.key via fsnotify and writes
	// a system.tls_reload_failed audit row on parse failures. The
	// actual cert swap lives in internal/certloader. Phase 6 Task 6.5.
	tlsReloader *tlsReloader
	// mdnsRefresher polls setup-status every 30s and re-publishes the
	// mDNS TXT records when state flips. Phase 6 Task 6.6.
	mdnsRefresher *mdnsRefresher

	// fndCtx + fndCtxCancel scope every Phase 6 goroutine so a
	// graceful shutdown stops them deterministically. Initialized at
	// the bottom of the foundation-services block in createResources.
	fndCtx       context.Context
	fndCtxCancel func()

	// in
	chAPIConfigSet chan *conf.Conf

	// out
	done chan struct{}
}

// New allocates a Core.
func New(args []string) (*Core, bool) {
	parser, err := kong.New(&cli,
		kong.Description("MediaMTX "+string(version)+", "+runtime.GOOS+", "+getArch()),
		kong.UsageOnError(),
		kong.ValueFormatter(func(value *kong.Value) string {
			switch value.Name {
			case "confpath":
				return "path to a config file. The default is mediamtx.yml."

			default:
				return kong.DefaultHelpValueFormatter(value)
			}
		}))
	if err != nil {
		panic(err)
	}

	_, err = parser.Parse(args)
	parser.FatalIfErrorf(err)

	if cli.Version {
		fmt.Println(string(version))
		os.Exit(0)
	}

	if cli.Upgrade {
		err = upgrade() //nolint:staticcheck
		if err != nil { //nolint:staticcheck
			fmt.Printf("ERR: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	ctx, ctxCancel := context.WithCancel(context.Background())

	p := &Core{
		ctx:            ctx,
		ctxCancel:      ctxCancel,
		chAPIConfigSet: make(chan *conf.Conf),
		done:           make(chan struct{}),
	}

	tempLogger := &logger.Logger{
		Level:        logger.Warn,
		Destinations: []logger.Destination{logger.DestinationStdout},
		Structured:   false,
		File:         "",
		SysLogPrefix: "",
	}
	tempLogger.Initialize() //nolint:errcheck

	confPaths := append([]string(nil), defaultConfPaths...)
	if runtime.GOOS != "windows" {
		confPaths = append(confPaths, defaultConfPathsNotWin...)
	}

	p.conf, p.confPath, err = conf.Load(cli.Confpath, confPaths, tempLogger)
	if err != nil {
		fmt.Printf("ERR: %s\n", err)
		return nil, false
	}

	err = p.createResources(true)
	if err != nil {
		if p.logger != nil {
			p.Log(logger.Error, "%s", err)
		} else {
			fmt.Printf("ERR: %s\n", err)
		}
		p.closeResources(nil, false)
		return nil, false
	}

	go p.run()

	return p, true
}

// Close closes Core and waits for all goroutines to return.
func (p *Core) Close() {
	p.ctxCancel()
	<-p.done
}

// Wait waits for the Core to exit.
func (p *Core) Wait() {
	<-p.done
}

// Log implements logger.Writer.
func (p *Core) Log(level logger.Level, format string, args ...any) {
	if p == nil || p.logger == nil {
		return
	}
	p.logger.Log(level, format, args...)
}

func (p *Core) run() {
	defer close(p.done)

	confChanged := func() chan struct{} {
		if p.confWatcher != nil {
			return p.confWatcher.Watch()
		}
		return make(chan struct{})
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	if runtime.GOOS == "linux" {
		signal.Notify(interrupt, syscall.SIGTERM)
	}

outer:
	for {
		select {
		case <-confChanged:
			p.Log(logger.Info, "reloading configuration (file changed)")

			newConf, _, err := conf.Load(p.confPath, nil, p.logger)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

			err = p.reloadConf(newConf, false)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

		case newConf := <-p.chAPIConfigSet:
			p.Log(logger.Info, "reloading configuration (API request)")

			err := p.reloadConf(newConf, true)
			if err != nil {
				p.Log(logger.Error, "%s", err)
				break outer
			}

			// Persist the just-applied conf to disk so API-driven
			// edits (cameras, recording policies, identity, etc.)
			// survive a recorder restart. Without this, MediaMTX's
			// inherited behavior treats every APIConfigSet as
			// in-memory only, and the operator has to re-add every
			// camera after every restart.
			//
			// Persistence failure is logged but does not roll back
			// in-memory state — the reload already succeeded, the
			// recorder is happily running the new conf, and rolling
			// back would mean tearing down running camera sources
			// for a reason the operator has no chance to fix mid-
			// request. The error surfaces in the log so disk
			// problems become visible.
			if p.confPath != "" {
				yamlBytes, saveErr := p.conf.SaveToFile(p.confPath)
				if saveErr != nil {
					p.Log(logger.Error,
						"failed to persist API-driven config to %s: %s",
						p.confPath, saveErr)
				} else if p.confWatcher != nil {
					// Note the just-written content so confwatcher's
					// fsnotify fire on our own write doesn't loop us
					// back into another reload.
					p.confWatcher.NoteSelfWrite(yamlBytes)
				}
			}

		case <-interrupt:
			p.Log(logger.Info, "shutting down gracefully")
			break outer

		case <-p.ctx.Done():
			break outer
		}
	}

	p.ctxCancel()

	p.closeResources(nil, false)
}

func (p *Core) createResources(initial bool) error {
	var err error

	if p.logger == nil {
		i := &logger.Logger{
			Level:        logger.Level(p.conf.LogLevel),
			Destinations: p.conf.LogDestinations.ToDestinations(),
			Structured:   p.conf.LogStructured,
			File:         p.conf.LogFile,
			SysLogPrefix: p.conf.SysLogPrefix,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.logger = i
	}

	if initial {
		p.Log(logger.Info, "MediaMTX %s, %s, %s", string(version), runtime.GOOS, getArch())

		if p.confPath != "" {
			a, _ := filepath.Abs(p.confPath)
			p.Log(logger.Info, "configuration loaded from %s", a)
		} else {
			list := make([]string, len(defaultConfPaths))
			for i, pa := range defaultConfPaths {
				a, _ := filepath.Abs(pa)
				list[i] = a
			}

			p.Log(logger.Warn,
				"configuration file not found (looked in %s), using an empty configuration",
				strings.Join(list, ", "))
		}

		// on Linux, try to raise the number of file descriptors that can be opened
		// to allow the maximum possible number of clients.
		rlimit.Raise() //nolint:errcheck

		gin.SetMode(gin.ReleaseMode)

		p.externalCmdPool = &externalcmd.Pool{}
		p.externalCmdPool.Initialize()
	}

	if p.identity == nil {
		idDir := p.conf.IdentityDir
		if idDir == "" {
			// Derive from the conf-file's parent directory. If
			// confPath is empty (recorder running without a conf file),
			// use "./identity" relative to cwd as a last resort.
			if p.confPath != "" {
				idDir = filepath.Join(filepath.Dir(p.confPath), "identity")
			} else {
				idDir = "identity"
			}
		}
		id, err := identity.Open(idDir)
		if err != nil {
			return fmt.Errorf("identity open: %w", err)
		}
		p.identity = id
		p.Log(logger.Info, "recorder identity loaded: id=%s dir=%s",
			id.ID().String(), idDir)
	}

	// Recorder-local LocalUser auth (pre-pairing auth slice 2026-05-06).
	// Stores LocalUser rows + recorder-local JWT signing key alongside
	// the device identity. The bootstrap admin is now seeded by the
	// canonical internal/bootstrap package (Phase 6); the localauth.New
	// constructor is the runtime token issuer + login flow only.
	if p.localAuth == nil {
		idDir := p.conf.IdentityDir
		if idDir == "" {
			if p.confPath != "" {
				idDir = filepath.Join(filepath.Dir(p.confPath), "identity")
			} else {
				idDir = "identity"
			}
		}
		s, err := recstore.Open(filepath.Join(idDir, "recorder.db"))
		if err != nil {
			return fmt.Errorf("local auth store open: %w", err)
		}
		p.localAuthStore = s
		signingKey, err := localauth.LoadOrCreateSigningKey(idDir)
		if err != nil {
			return fmt.Errorf("local auth signing key: %w", err)
		}

		// Phase 6 Task 6.2 + 6.3: bootstrap admin + system-settings
		// runtime defaults. The bootstrap package is idempotent so a
		// re-Open of an existing recorder is a no-op.
		bootRes, err := bootstrap.Run(p.ctx, s, idDir, bootstrap.Options{
			RecordingsRoot: defaultRecordingsRoot(p.conf),
		})
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		if bootRes.AdminCreated && bootRes.InitialPassword != "" {
			banner := strings.Repeat("=", 72)
			p.Log(logger.Info,
				"\n%s\nRECORDER BOOTSTRAP ADMIN CREATED\n  username:        admin\n  initial password: %s\n  must_change_password: %t (forced rotation on first login)\n  also written to: %s/%s (mode 0600)\nLog in at https://<this-host>:9997/ to complete setup.\n%s",
				banner, bootRes.InitialPassword, bootRes.MustChangePassword,
				idDir, "initial-admin-password.txt", banner)
		} else if bootRes.AdminCreated {
			p.Log(logger.Info,
				"recorder bootstrap admin created using RAIKADA_BOOTSTRAP_PASSWORD env var")
		}

		p.localAuth = localauth.New(s, signingKey, idDir, "", p.identity.ID().String())
	}

	// Phase 6 Task 6.4: foundation services wiring. Construct once,
	// re-use across conf reloads. Uses the recorder's localAuthStore
	// (the same SQLite handle the API surface depends on) so all
	// foundation packages share one backing store.
	if p.fndCtxCancel == nil {
		p.fndCtx, p.fndCtxCancel = context.WithCancel(context.Background())
	}
	if p.credVault == nil {
		idDir := p.conf.IdentityDir
		if idDir == "" {
			if p.confPath != "" {
				idDir = filepath.Join(filepath.Dir(p.confPath), "identity")
			} else {
				idDir = "identity"
			}
		}
		v, err := cameracred.Open(idDir)
		if err != nil {
			return fmt.Errorf("cameracred open: %w", err)
		}
		p.credVault = v
	}
	if p.auditEmit == nil && p.localAuthStore != nil {
		p.auditEmit = audit.New(p.localAuthStore.AuditLog)
	}

	if p.camerasBus == nil {
		p.camerasBus = cameras.NewBus()
	}
	if p.eventsBus == nil {
		p.eventsBus = events.NewBus()
	}

	// Camera service. Onvif teardown is wired below once the onvif
	// manager exists; until then the service uses a nil teardown which
	// is benign (Delete logs a debug warning instead of unsubscribing).
	if p.camerasService == nil && p.localAuthStore != nil {
		p.camerasService = cameras.NewService(p.localAuthStore, p.credVault, p.camerasBus, nil)
	}

	// Events service. Cloud outbox enqueuer is the foundation
	// CloudOutboxRepo (a one-method interface).
	if p.eventsService == nil && p.localAuthStore != nil {
		p.eventsService = events.NewService(
			p.localAuthStore.Events,
			p.localAuthStore.EventRetention,
			p.localAuthStore.CloudOutbox,
			p.eventsBus,
		)
	}

	// Schedule resolver — motion controller is wired later in this
	// function (Wave 4 motion controller block); we pass nil here
	// because the resolver's New tolerates nil and the foundation
	// motion controller is not currently feeding the resolver.
	if p.scheduleResolver == nil && p.localAuthStore != nil {
		p.scheduleResolver = schedule.New(
			p.localAuthStore.Cameras,
			p.localAuthStore.RecordingPolicies,
			p.localAuthStore.RecordingSchedules,
			p.localAuthStore.SystemSettings,
			nil, // motion controller (set up later in this fn)
		)
	}

	// Notifications dispatcher.
	if p.notifDispatcher == nil && p.eventsService != nil {
		site := notifications.SitePayload{
			ID:   p.identity.ID().String(),
			Name: getSiteName(p.fndCtx, p.localAuthStore),
		}
		// Token issuer for signed snapshot URLs. Phase 6 leaves it
		// nil — adding a per-event short-lived JWT is a follow-up; for
		// now URLs surface as plain (the SPA opens them with a session
		// cookie).
		signURL := makeSignedURL(p.conf.APIAddress, nil)
		p.notifDispatcher = notifications.NewDispatcher(
			p.localAuthStore, p.credVault, p.eventsService,
			site, signURL, p,
		)
		go p.notifDispatcher.Run(p.fndCtx)
	}

	// Retention manager: segments + events + clips sweepers.
	if p.retentionMgr == nil && p.localAuthStore != nil {
		segLister := newSegmentListerAdapter(
			func() map[string]*conf.Path { return snapshotPathConfs(p.pathManager) },
			resolveCameraName(p.localAuthStore.Cameras),
			p,
		)
		segs := retention.NewSegmentsSweeper(
			p.localAuthStore.Cameras,
			p.localAuthStore.RecordingPolicies,
			segLister,
			0, p,
		)
		evs := retention.NewEventsSweeper(p.localAuthStore.Events, 0, 0, p)
		clips := retention.NewClipsSweeper(p.localAuthStore.Clips, 0, 0, p)
		p.retentionMgr = retention.NewManager(p, segs, evs, clips)
		go p.retentionMgr.Run(p.fndCtx)
	}

	// Cloud bridge: foundation ships a nop processor + horizon sweeper.
	if p.cloudSvc == nil && p.localAuthStore != nil {
		hSweeper := cloudbridge.NewHorizonSweeper(
			p.localAuthStore.CloudOutbox,
			func(ctx context.Context) int {
				n, _ := p.localAuthStore.SystemSettings.GetInt(ctx, "cloud_outbox_horizon_hours", 168)
				return n
			},
			0, p,
		)
		p.cloudSvc = cloudbridge.NewService(cloudbridge.NewNopProcessor(), hSweeper, p)
		go p.cloudSvc.Run(p.fndCtx)
	}

	if p.mdnsService == nil && p.conf.MDNS != nil && *p.conf.MDNS && p.conf.API {
		// Use the API listen port for mDNS announcements — that's
		// the surface clients will reach the recorder on. ":port"
		// stripping is sufficient because APIAddress is "host:port"
		// or ":port".
		port := parseAPIPort(p.conf.APIAddress)
		p.mdnsService = mdns.New(p.identity, p, string(version), port)
		if err := p.mdnsService.Start(); err != nil {
			p.Log(logger.Warn, "mdns failed to start: %s", err)
			p.mdnsService = nil
		}
	}

	// Phase 6 Task 6.5: TLS reload audit watcher. The actual cert swap
	// lives in internal/certloader (already wired through httpp.Server);
	// this watcher writes the system.tls_reload_failed audit row on
	// parse failures so the operator-visible chain records the event.
	if p.tlsReloader == nil && p.identity != nil && p.auditEmit != nil {
		certPath, keyPath := p.identity.TLSPaths()
		p.tlsReloader = newTLSReloader(certPath, keyPath, p.auditEmit, p)
		go p.tlsReloader.Run(p.fndCtx)
	}

	// Phase 6 Task 6.6: mDNS TXT-record refresher. Polls setup state
	// every 30s and republishes when state flips (setup-required ↔
	// setup-complete). Only runs when the mDNS broadcaster is up.
	if p.mdnsRefresher == nil && p.mdnsService != nil && p.localAuthStore != nil {
		p.mdnsRefresher = newMDNSRefresher(
			p.mdnsService, p.localAuthStore,
			string(version), p.identity.ID().String(), p,
		)
		go p.mdnsRefresher.Run(p.fndCtx)
	}

	// TODO(phase6): wire CRL poller for cert revocation watch.

	if p.authManager == nil {
		la := p.localAuth
		p.authManager = &auth.Manager{
			Method:          p.conf.AuthMethod,
			InternalUsers:   p.conf.AuthInternalUsers,
			HTTPAddress:     p.conf.AuthHTTPAddress,
			HTTPFingerprint: p.conf.AuthHTTPFingerprint,
			HTTPExclude:     p.conf.AuthHTTPExclude,
			JWTClaimKey:     p.conf.AuthJWTClaimKey,
			JWTExclude:      p.conf.AuthJWTExclude,
			JWTInHTTPQuery:  p.conf.AuthJWTInHTTPQuery,
			JWTIssuer:       p.conf.AuthJWTIssuer,
			JWTAudience:     p.conf.AuthJWTAudience,
			ReadTimeout:     time.Duration(p.conf.ReadTimeout),
			// Recorder-local JWT validation hook. Lets the auth
			// manager accept JWTs minted by /v1/recorder/login even
			// when authMethod=internal in mediamtx.yml. Pre-pairing
			// auth slice 2026-05-06.
			LocalJWT: func() (any, string, string, bool) {
				if la == nil {
					return nil, "", "", false
				}
				sk := la.SigningKey()
				if sk == nil {
					return nil, "", "", false
				}
				return sk.PublicKey(), la.Issuer(), la.Audience(), true
			},
		}
	}

	// TODO(phase6): re-add pairing-aware auth wiring (override to JWT
	// using bound MS JWKS) once the new pairing module lands.

	if p.conf.Metrics &&
		p.metrics == nil {
		i := &metrics.Metrics{
			Address:        p.conf.MetricsAddress,
			DumpPackets:    p.conf.DumpPackets,
			Encryption:     p.conf.MetricsEncryption,
			ServerKey:      p.conf.MetricsServerKey,
			ServerCert:     p.conf.MetricsServerCert,
			AllowOrigins:   p.conf.MetricsAllowOrigins,
			TrustedProxies: p.conf.MetricsTrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			AuthManager:    p.authManager,
			Parent:         p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.metrics = i
	}

	if p.conf.PPROF &&
		p.pprof == nil {
		i := &pprof.PPROF{
			Address:        p.conf.PPROFAddress,
			DumpPackets:    p.conf.DumpPackets,
			Encryption:     p.conf.PPROFEncryption,
			ServerKey:      p.conf.PPROFServerKey,
			ServerCert:     p.conf.PPROFServerCert,
			AllowOrigins:   p.conf.PPROFAllowOrigins,
			TrustedProxies: p.conf.PPROFTrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			AuthManager:    p.authManager,
			Parent:         p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.pprof = i
	}

	if p.recordCleaner == nil &&
		atLeastOneRecordDeleteAfter(p.conf.Paths) {
		p.recordCleaner = &recordcleaner.Cleaner{
			PathConfs:             p.conf.Paths,
			Parent:                p,
			PublishVolumeFull:     api.PublishStorageVolumeFull,
			PublishVolumeDegraded: api.PublishStorageVolumeDegraded,
		}
		p.recordCleaner.Initialize()
	}

	if p.conf.Playback &&
		p.playbackServer == nil {
		i := &playback.Server{
			Address:        p.conf.PlaybackAddress,
			DumpPackets:    p.conf.DumpPackets,
			Encryption:     p.conf.PlaybackEncryption,
			ServerKey:      p.conf.PlaybackServerKey,
			ServerCert:     p.conf.PlaybackServerCert,
			AllowOrigins:   p.conf.PlaybackAllowOrigins,
			TrustedProxies: p.conf.PlaybackTrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			PathConfs:      p.conf.Paths,
			AuthManager:    p.authManager,
			Parent:         p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.playbackServer = i
	}

	if p.pathManager == nil {
		rtpMaxPayloadSize := getRTPMaxPayloadSize(p.conf.UDPMaxPayloadSize, p.conf.RTSPEncryption)

		p.pathManager = &pathManager{
			logLevel:          p.conf.LogLevel,
			dumpPackets:       p.conf.DumpPackets,
			rtspAddress:       p.conf.RTSPAddress,
			readTimeout:       p.conf.ReadTimeout,
			writeTimeout:      p.conf.WriteTimeout,
			writeQueueSize:    p.conf.WriteQueueSize,
			udpReadBufferSize: p.conf.UDPReadBufferSize,
			rtpMaxPayloadSize: rtpMaxPayloadSize,
			pathConfs:         p.conf.Paths,
			authManager:       p.authManager,
			externalCmdPool:   p.externalCmdPool,
			metrics:           p.metrics,
			parent:            p,
		}
		p.pathManager.initialize()
	}

	// Phase 6 Task 6.4: cameras → path-manager bridge. Built after the
	// path manager exists so the adapter can dispatch ReloadFromCameras.
	// Bootstrap pushes the existing camera set immediately; Run blocks
	// until the foundation context cancels.
	if p.pathBridge == nil && p.camerasService != nil && p.pathManager != nil {
		p.pmAdapter = newPathManagerAdapter(p.pathManager, pathDefaultsFromConf(p.conf), p)
		p.pmAdapter.RefreshDefaults(p.conf)
		p.pathBridge = cameras.NewPathBridge(p.camerasService, p.pmAdapter, p)
		if err := p.pathBridge.Bootstrap(p.fndCtx); err != nil {
			p.Log(logger.Warn, "[cameras.bridge] bootstrap: %v", err)
		}
		go p.pathBridge.Run(p.fndCtx)
	} else if p.pmAdapter != nil {
		// Every conf apply (API camera create/delete, SIGHUP, file
		// watch) re-runs createResources; fold the new paths into the
		// bridge adapter so post-boot cameras merge against their real
		// conf path instead of a nil base.
		p.pmAdapter.RefreshDefaults(p.conf)
		// Re-run the bridge bootstrap so a flush that raced this conf
		// apply (bus event before the adapter refresh) converges on the
		// refreshed defaults. Idempotent: list + merge + reload.
		if p.pathBridge != nil && p.fndCtx != nil {
			if err := p.pathBridge.Bootstrap(p.fndCtx); err != nil {
				p.Log(logger.Warn, "[cameras.bridge] re-bootstrap: %v", err)
			}
		}
	}

	if p.conf.RTSP &&
		(p.conf.RTSPEncryption == conf.EncryptionNo ||
			p.conf.RTSPEncryption == conf.EncryptionOptional) &&
		p.rtspServer == nil {
		udpReadBufferSize := p.conf.UDPReadBufferSize
		if p.conf.RTSPUDPReadBufferSize != nil {
			udpReadBufferSize = *p.conf.RTSPUDPReadBufferSize
		}

		i := &rtsp.Server{
			Address:             p.conf.RTSPAddress,
			AuthMethods:         p.conf.RTSPAuthMethods.ToAuthMethods(),
			DumpPackets:         p.conf.DumpPackets,
			UDPReadBufferSize:   udpReadBufferSize,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			WriteQueueSize:      p.conf.WriteQueueSize,
			RTSPTransports:      p.conf.RTSPTransports,
			RTPAddress:          p.conf.RTPAddress,
			RTCPAddress:         p.conf.RTCPAddress,
			MulticastIPRange:    p.conf.MulticastIPRange,
			MulticastRTPPort:    p.conf.MulticastRTPPort,
			MulticastRTCPPort:   p.conf.MulticastRTCPPort,
			Encryption:          false,
			ServerCert:          "",
			ServerKey:           "",
			RTSPAddress:         p.conf.RTSPAddress,
			Transports:          p.conf.RTSPTransports,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtspServer = i
	}

	if p.conf.RTSP &&
		(p.conf.RTSPEncryption == conf.EncryptionStrict ||
			p.conf.RTSPEncryption == conf.EncryptionOptional) &&
		p.rtspsServer == nil {
		udpReadBufferSize := p.conf.UDPReadBufferSize
		if p.conf.RTSPUDPReadBufferSize != nil {
			udpReadBufferSize = *p.conf.RTSPUDPReadBufferSize
		}

		i := &rtsp.Server{
			Address:             p.conf.RTSPSAddress,
			AuthMethods:         p.conf.RTSPAuthMethods.ToAuthMethods(),
			DumpPackets:         p.conf.DumpPackets,
			UDPReadBufferSize:   udpReadBufferSize,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			WriteQueueSize:      p.conf.WriteQueueSize,
			RTSPTransports:      p.conf.RTSPTransports,
			RTPAddress:          p.conf.SRTPAddress,
			RTCPAddress:         p.conf.SRTCPAddress,
			MulticastIPRange:    p.conf.MulticastIPRange,
			MulticastRTPPort:    p.conf.MulticastSRTPPort,
			MulticastRTCPPort:   p.conf.MulticastSRTCPPort,
			Encryption:          true,
			ServerCert:          p.conf.RTSPServerCert,
			ServerKey:           p.conf.RTSPServerKey,
			RTSPAddress:         p.conf.RTSPAddress,
			Transports:          p.conf.RTSPTransports,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtspsServer = i
	}

	if p.conf.RTMP &&
		(p.conf.RTMPEncryption == conf.EncryptionNo ||
			p.conf.RTMPEncryption == conf.EncryptionOptional) &&
		p.rtmpServer == nil {
		i := &rtmp.Server{
			Address:             p.conf.RTMPAddress,
			DumpPackets:         p.conf.DumpPackets,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			Encryption:          false,
			ServerCert:          "",
			ServerKey:           "",
			RTSPAddress:         p.conf.RTSPAddress,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtmpServer = i
	}

	if p.conf.RTMP &&
		(p.conf.RTMPEncryption == conf.EncryptionStrict ||
			p.conf.RTMPEncryption == conf.EncryptionOptional) &&
		p.rtmpsServer == nil {
		i := &rtmp.Server{
			Address:             p.conf.RTMPSAddress,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			Encryption:          true,
			ServerCert:          p.conf.RTMPServerCert,
			ServerKey:           p.conf.RTMPServerKey,
			DumpPackets:         p.conf.DumpPackets,
			RTSPAddress:         p.conf.RTSPAddress,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.rtmpsServer = i
	}

	if p.conf.HLS &&
		p.hlsServer == nil {
		i := &hls.Server{
			Address:         p.conf.HLSAddress,
			DumpPackets:     p.conf.DumpPackets,
			Encryption:      p.conf.HLSEncryption,
			ServerKey:       p.conf.HLSServerKey,
			ServerCert:      p.conf.HLSServerCert,
			AllowOrigins:    p.conf.HLSAllowOrigins,
			TrustedProxies:  p.conf.HLSTrustedProxies,
			AlwaysRemux:     p.conf.HLSAlwaysRemux,
			Variant:         p.conf.HLSVariant,
			SegmentCount:    p.conf.HLSSegmentCount,
			SegmentDuration: p.conf.HLSSegmentDuration,
			PartDuration:    p.conf.HLSPartDuration,
			SegmentMaxSize:  p.conf.HLSSegmentMaxSize,
			Directory:       p.conf.HLSDirectory,
			ReadTimeout:     p.conf.ReadTimeout,
			WriteTimeout:    p.conf.WriteTimeout,
			MuxerCloseAfter: p.conf.HLSMuxerCloseAfter,
			ExternalCmdPool: p.externalCmdPool,
			Metrics:         p.metrics,
			PathManager:     p.pathManager,
			Parent:          p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.hlsServer = i
	}

	if p.conf.WebRTC &&
		p.webRTCServer == nil {
		i := &webrtc.Server{
			Address:               p.conf.WebRTCAddress,
			DumpPackets:           p.conf.DumpPackets,
			Encryption:            p.conf.WebRTCEncryption,
			ServerKey:             p.conf.WebRTCServerKey,
			ServerCert:            p.conf.WebRTCServerCert,
			AllowOrigins:          p.conf.WebRTCAllowOrigins,
			TrustedProxies:        p.conf.WebRTCTrustedProxies,
			ReadTimeout:           p.conf.ReadTimeout,
			WriteTimeout:          p.conf.WriteTimeout,
			UDPReadBufferSize:     p.conf.UDPReadBufferSize,
			LocalUDPAddress:       p.conf.WebRTCLocalUDPAddress,
			LocalTCPAddress:       p.conf.WebRTCLocalTCPAddress,
			IPsFromInterfaces:     p.conf.WebRTCIPsFromInterfaces,
			IPsFromInterfacesList: p.conf.WebRTCIPsFromInterfacesList,
			AdditionalHosts:       p.conf.WebRTCAdditionalHosts,
			ICEServers:            p.conf.WebRTCICEServers2,
			STUNGatherTimeout:     p.conf.WebRTCSTUNGatherTimeout,
			HandshakeTimeout:      p.conf.WebRTCHandshakeTimeout,
			TrackGatherTimeout:    p.conf.WebRTCTrackGatherTimeout,
			ExternalCmdPool:       p.externalCmdPool,
			Metrics:               p.metrics,
			PathManager:           p.pathManager,
			Parent:                p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.webRTCServer = i
	}

	if p.conf.SRT &&
		p.srtServer == nil {
		i := &srt.Server{
			Address:             p.conf.SRTAddress,
			RTSPAddress:         p.conf.RTSPAddress,
			ReadTimeout:         p.conf.ReadTimeout,
			WriteTimeout:        p.conf.WriteTimeout,
			UDPMaxPayloadSize:   p.conf.UDPMaxPayloadSize,
			RunOnConnect:        p.conf.RunOnConnect,
			RunOnConnectRestart: p.conf.RunOnConnectRestart,
			RunOnDisconnect:     p.conf.RunOnDisconnect,
			ExternalCmdPool:     p.externalCmdPool,
			Metrics:             p.metrics,
			PathManager:         p.pathManager,
			Parent:              p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.srtServer = i
	}

	if p.conf.API &&
		p.api == nil {
		// API server: certificate paths default to identity-managed
		// tls.crt + tls.key when not operator-overridden in conf.
		certPath := p.conf.APIServerCert
		keyPath := p.conf.APIServerKey
		if (certPath == "" || keyPath == "" ||
			certPath == "identity/api-server.crt" || keyPath == "identity/recorder.key") &&
			p.identity != nil {
			tlsCert, tlsKey := p.identity.TLSPaths()
			certPath = tlsCert
			keyPath = tlsKey
		}

		i := &api.API{
			Version:        string(version),
			Started:        started,
			Address:        p.conf.APIAddress,
			DumpPackets:    p.conf.DumpPackets,
			Encryption:     p.conf.APIEncryption,
			ServerKey:      keyPath,
			ServerCert:     certPath,
			AllowOrigins:   p.conf.APIAllowOrigins,
			TrustedProxies: p.conf.APITrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			Conf:           p.conf,
			AuthManager:    p.authManager,
			Identity:       p.identity,
			LocalAuth:      p.localAuth,
			MDNS:           p.mdnsService,
			PathManager:    p.pathManager,
			RTSPServer:     p.rtspServer,
			RTSPSServer:    p.rtspsServer,
			RTMPServer:     p.rtmpServer,
			RTMPSServer:    p.rtmpsServer,
			HLSServer:      p.hlsServer,
			WebRTCServer:   p.webRTCServer,
			SRTServer:      p.srtServer,
			Parent:         p,

			// Phase 6 foundation services. Handlers defensively check
			// for nil so the API still serves /v1/info and the SPA
			// even if a service failed to wire.
			Store:            p.localAuthStore,
			Vault:            p.credVault,
			CamerasService:   p.camerasService,
			EventsService:    p.eventsService,
			ScheduleResolver: p.scheduleResolver,
			NotifDispatcher:  p.notifDispatcher,
			RetentionMgr:     p.retentionMgr,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.api = i

		// Wire the localauth manager's audit callback so login /
		// failed-login attempts land in the recorder's per-emitter
		// audit chain (ADR 0006 D1). Done after both p.api and
		// p.localAuth exist.
		if p.localAuth != nil {
			p.localAuth.SetAuditEmitter(p.api.EmitLocalAuthAudit)
		}

		// TODO(phase6): wire camera-sync, policy-sync, update-poll
		// adapters once the new pairing module + post-pair callbacks
		// exist. They previously polled the bound MS and dispatched
		// drift through p.api's camera/policy applier hooks.

		// Wave 6: software-update applier wiring per ADR 0014. Wired
		// only when the operator has provisioned a Raikada release
		// public key in conf; without one the apply endpoint surfaces
		// a stable 503.
		if p.conf.RaikadaUpdatePublicKey != "" && p.identity != nil {
			pubKey, err := softwareupdate.LoadPublicKeyB64(p.conf.RaikadaUpdatePublicKey)
			if err != nil {
				p.Log(logger.Warn, "[softwareupdate] invalid public key, applier disabled: %s", err)
			} else {
				stateDir := p.conf.IdentityDir
				if stateDir == "" {
					if p.confPath != "" {
						stateDir = filepath.Join(filepath.Dir(p.confPath), "identity")
					} else {
						stateDir = "identity"
					}
				}
				stateStore, err := softwareupdate.NewStateStore(stateDir)
				if err != nil {
					p.Log(logger.Warn, "[softwareupdate] state store init failed: %s", err)
				} else {
					binPath, _ := os.Executable()
					applier := &softwareupdate.Applier{
						BinaryPath:     binPath,
						PublicKey:      pubKey,
						State:          stateStore,
						Logger:         p,
						Audit:          p.api.SoftwareUpdateAuditEmitter(),
						CurrentVersion: string(version),
						SignalSelf: func() error {
							// Send SIGTERM to self so the host's process
							// supervisor (systemd / launchd / docker
							// restart-policy) relaunches with the new
							// binary. Recorders run without a supervisor
							// will exit cleanly and not relaunch — this
							// is the documented v1 limitation.
							pr, err := os.FindProcess(os.Getpid())
							if err != nil {
								return err
							}
							return pr.Signal(syscall.SIGTERM)
						},
					}
					p.api.SetSoftwareUpdateApplier(func(_ *gin.Context, opts softwareupdate.ApplyOptions) error {
						return applier.Apply(context.Background(), opts)
					})
				}
			}
		}

		// TODO(phase6): re-wire pairing-manager audit + paired-callback
		// fan-out (mDNS refresh, CRL poller Start, camera/policy/update
		// pollers Start) once the new pairing module lands.

		// Wire the pipeline-side Event publish target so path.go's
		// camera.online / camera.offline emitters land in the same
		// EventStore that /v1/events serves from. Consumer NVR is
		// single-tenant; an empty tenant id is fine.
		api.SetPipelineEventTarget(api.DefaultEventStore(), func() string {
			return ""
		})

		// Wave A3: per-segment historical policy resolver. path.go's
		// OnSegmentComplete closure calls recordingmeta.Write at seal
		// time. The closure captures the current conf pointer by value
		// (mirroring the SetPipelineEventTarget pattern above); a conf
		// reload runs createResources again and re-installs a fresh
		// closure pointing at the new conf. No mutex needed because
		// each closure holds its own immutable snapshot.
		confSnap := p.conf
		recordingmeta.SetResolver(func(pathName string) (recordingmeta.Sidecar, bool) {
			return resolveRecordingMeta(confSnap, pathName)
		})

		// Wire the recorder-side segment.write_failed publisher to
		// land emissions in the same EventStore as the rest of the
		// pipeline-side kinds. recorder_instance.run() invokes the
		// hook only when errors.As detects a *SegmentWriteError, so
		// non-write failures (network, codec) do not surface as a
		// canonical write-failed event.
		recorder.SetSegmentWriteFailedPublisher(api.PublishSegmentWriteFailed)

		// ONVIF subscription manager (Wave 3). One process-wide
		// manager owns active PullPoint subscriptions; the API
		// handlers in /v1/onvif/event-subscriptions interact with
		// it. Events from cameras flow through the manager's sink
		// into publishOnvifEvent, which reuses the pipeline's
		// EventStore.
		if p.onvifManager == nil {
			p.onvifManager = onvif.NewManager(p, func(ev onvif.EventNotification) {
				api.PublishOnvifEvent(ev)
			}, nil)
			// TODO(phase6): re-attach the subscription persister + rehydrate
			// once the post-MS persistence layer ships.
			api.SetOnvifManager(p.onvifManager)

			// Phase 6: rebuild the cameras.Service with the onvif
			// teardown adapter now that the manager exists. The
			// service was constructed with a nil teardown earlier so
			// the path bridge could come up before onvif; we swap in
			// a wired service so Camera.Delete unsubscribes cleanly.
			if p.camerasService != nil && p.localAuthStore != nil {
				teardown := newOnvifTeardownAdapter(p.onvifManager, p)
				p.camerasService = cameras.NewService(
					p.localAuthStore, p.credVault, p.camerasBus, teardown,
				)
			}
		}

		// Motion controller (Wave 4). Subscribes to the EventStore
		// for camera.motion_detected events; emits
		// recording.motion_started / motion_ended back into the
		// same store. Construction is idempotent — wiring on every
		// createResources call replaces the previous controller and
		// re-registers a subscriber. We accept the duplicate
		// subscriber on conf reload as the cost of avoiding a
		// finer-grained "swap subscriber" API; the duplicate fan-out
		// is harmless because the controller dedupes via its
		// per-camera active map.
		if p.motionController != nil {
			p.motionController.Close()
		}
		p.motionController = api.WireMotionControllerForAPI(p.api, p)
	}

	if initial && p.confPath != "" {
		cf := &confwatcher.ConfWatcher{FilePath: p.confPath}
		err = cf.Initialize()
		if err != nil {
			return err
		}
		p.confWatcher = cf
	}

	return nil
}

func (p *Core) closeResources(newConf *conf.Conf, calledByAPI bool) {
	closeLogger := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		!reflect.DeepEqual(newConf.LogDestinations, p.conf.LogDestinations) ||
		newConf.LogFile != p.conf.LogFile ||
		newConf.SysLogPrefix != p.conf.SysLogPrefix ||
		newConf.LogStructured != p.conf.LogStructured

	closeAuthManager := newConf == nil ||
		newConf.AuthMethod != p.conf.AuthMethod ||
		newConf.AuthHTTPAddress != p.conf.AuthHTTPAddress ||
		newConf.AuthHTTPFingerprint != p.conf.AuthHTTPFingerprint ||
		!reflect.DeepEqual(newConf.AuthHTTPExclude, p.conf.AuthHTTPExclude) ||
		newConf.AuthJWTClaimKey != p.conf.AuthJWTClaimKey ||
		!reflect.DeepEqual(newConf.AuthJWTExclude, p.conf.AuthJWTExclude) ||
		!reflect.DeepEqual(newConf.AuthJWTInHTTPQuery, p.conf.AuthJWTInHTTPQuery) ||
		newConf.AuthJWTIssuer != p.conf.AuthJWTIssuer ||
		newConf.AuthJWTAudience != p.conf.AuthJWTAudience ||
		newConf.ReadTimeout != p.conf.ReadTimeout
	if !closeAuthManager && !reflect.DeepEqual(newConf.AuthInternalUsers, p.conf.AuthInternalUsers) {
		p.authManager.ReloadInternalUsers(newConf.AuthInternalUsers)
	}

	closeMetrics := newConf == nil ||
		newConf.Metrics != p.conf.Metrics ||
		newConf.MetricsAddress != p.conf.MetricsAddress ||
		newConf.MetricsEncryption != p.conf.MetricsEncryption ||
		newConf.MetricsServerKey != p.conf.MetricsServerKey ||
		newConf.MetricsServerCert != p.conf.MetricsServerCert ||
		!slices.Equal(newConf.MetricsAllowOrigins, p.conf.MetricsAllowOrigins) ||
		!reflect.DeepEqual(newConf.MetricsTrustedProxies, p.conf.MetricsTrustedProxies) ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closeAuthManager ||
		closeLogger

	closePPROF := newConf == nil ||
		newConf.PPROF != p.conf.PPROF ||
		newConf.PPROFAddress != p.conf.PPROFAddress ||
		newConf.PPROFEncryption != p.conf.PPROFEncryption ||
		newConf.PPROFServerKey != p.conf.PPROFServerKey ||
		newConf.PPROFServerCert != p.conf.PPROFServerCert ||
		!slices.Equal(newConf.PPROFAllowOrigins, p.conf.PPROFAllowOrigins) ||
		!reflect.DeepEqual(newConf.PPROFTrustedProxies, p.conf.PPROFTrustedProxies) ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closeAuthManager ||
		closeLogger

	closeRecorderCleaner := newConf == nil ||
		atLeastOneRecordDeleteAfter(newConf.Paths) != atLeastOneRecordDeleteAfter(p.conf.Paths) ||
		closeLogger
	if !closeRecorderCleaner && p.recordCleaner != nil && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.recordCleaner.ReloadPathConfs(newConf.Paths)
	}

	closePlaybackServer := newConf == nil ||
		newConf.Playback != p.conf.Playback ||
		newConf.PlaybackAddress != p.conf.PlaybackAddress ||
		newConf.PlaybackEncryption != p.conf.PlaybackEncryption ||
		newConf.PlaybackServerKey != p.conf.PlaybackServerKey ||
		newConf.PlaybackServerCert != p.conf.PlaybackServerCert ||
		!slices.Equal(newConf.PlaybackAllowOrigins, p.conf.PlaybackAllowOrigins) ||
		!reflect.DeepEqual(newConf.PlaybackTrustedProxies, p.conf.PlaybackTrustedProxies) ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closeAuthManager ||
		closeLogger
	if !closePlaybackServer && p.playbackServer != nil && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.playbackServer.ReloadPathConfs(newConf.Paths)
	}

	closePathManager := newConf == nil ||
		newConf.LogLevel != p.conf.LogLevel ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.WriteQueueSize != p.conf.WriteQueueSize ||
		newConf.UDPReadBufferSize != p.conf.UDPReadBufferSize ||
		newConf.UDPMaxPayloadSize != p.conf.UDPMaxPayloadSize ||
		newConf.RTSPEncryption != p.conf.RTSPEncryption ||
		closeMetrics ||
		closeAuthManager ||
		closeLogger
	if !closePathManager && !reflect.DeepEqual(newConf.Paths, p.conf.Paths) {
		p.pathManager.ReloadPathConfs(newConf.Paths)
	}

	closeRTSPServer := newConf == nil ||
		newConf.RTSP != p.conf.RTSP ||
		newConf.RTSPEncryption != p.conf.RTSPEncryption ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		!reflect.DeepEqual(newConf.RTSPAuthMethods, p.conf.RTSPAuthMethods) ||
		newConf.RTSPUDPReadBufferSize != p.conf.RTSPUDPReadBufferSize ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		newConf.UDPReadBufferSize != p.conf.UDPReadBufferSize ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.WriteQueueSize != p.conf.WriteQueueSize ||
		newConf.RTPAddress != p.conf.RTPAddress ||
		newConf.RTCPAddress != p.conf.RTCPAddress ||
		newConf.MulticastIPRange != p.conf.MulticastIPRange ||
		newConf.MulticastRTPPort != p.conf.MulticastRTPPort ||
		newConf.MulticastRTCPPort != p.conf.MulticastRTCPPort ||
		!reflect.DeepEqual(newConf.RTSPTransports, p.conf.RTSPTransports) ||
		newConf.RunOnConnect != p.conf.RunOnConnect ||
		newConf.RunOnConnectRestart != p.conf.RunOnConnectRestart ||
		newConf.RunOnDisconnect != p.conf.RunOnDisconnect ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeRTSPSServer := newConf == nil ||
		newConf.RTSP != p.conf.RTSP ||
		newConf.RTSPEncryption != p.conf.RTSPEncryption ||
		newConf.RTSPSAddress != p.conf.RTSPSAddress ||
		!reflect.DeepEqual(newConf.RTSPAuthMethods, p.conf.RTSPAuthMethods) ||
		newConf.RTSPUDPReadBufferSize != p.conf.RTSPUDPReadBufferSize ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		newConf.UDPReadBufferSize != p.conf.UDPReadBufferSize ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.WriteQueueSize != p.conf.WriteQueueSize ||
		newConf.RTSPServerCert != p.conf.RTSPServerCert ||
		newConf.RTSPServerKey != p.conf.RTSPServerKey ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		!reflect.DeepEqual(newConf.RTSPTransports, p.conf.RTSPTransports) ||
		newConf.RunOnConnect != p.conf.RunOnConnect ||
		newConf.RunOnConnectRestart != p.conf.RunOnConnectRestart ||
		newConf.RunOnDisconnect != p.conf.RunOnDisconnect ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeRTMPServer := newConf == nil ||
		newConf.RTMP != p.conf.RTMP ||
		newConf.RTMPEncryption != p.conf.RTMPEncryption ||
		newConf.RTMPAddress != p.conf.RTMPAddress ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		newConf.RunOnConnect != p.conf.RunOnConnect ||
		newConf.RunOnConnectRestart != p.conf.RunOnConnectRestart ||
		newConf.RunOnDisconnect != p.conf.RunOnDisconnect ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeRTMPSServer := newConf == nil ||
		newConf.RTMP != p.conf.RTMP ||
		newConf.RTMPEncryption != p.conf.RTMPEncryption ||
		newConf.RTMPSAddress != p.conf.RTMPSAddress ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.RTMPServerCert != p.conf.RTMPServerCert ||
		newConf.RTMPServerKey != p.conf.RTMPServerKey ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		newConf.RunOnConnect != p.conf.RunOnConnect ||
		newConf.RunOnConnectRestart != p.conf.RunOnConnectRestart ||
		newConf.RunOnDisconnect != p.conf.RunOnDisconnect ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeHLSServer := newConf == nil ||
		newConf.HLS != p.conf.HLS ||
		newConf.HLSAddress != p.conf.HLSAddress ||
		newConf.HLSEncryption != p.conf.HLSEncryption ||
		newConf.HLSServerKey != p.conf.HLSServerKey ||
		newConf.HLSServerCert != p.conf.HLSServerCert ||
		!slices.Equal(newConf.HLSAllowOrigins, p.conf.HLSAllowOrigins) ||
		!reflect.DeepEqual(newConf.HLSTrustedProxies, p.conf.HLSTrustedProxies) ||
		newConf.HLSAlwaysRemux != p.conf.HLSAlwaysRemux ||
		newConf.HLSVariant != p.conf.HLSVariant ||
		newConf.HLSSegmentCount != p.conf.HLSSegmentCount ||
		newConf.HLSSegmentDuration != p.conf.HLSSegmentDuration ||
		newConf.HLSPartDuration != p.conf.HLSPartDuration ||
		newConf.HLSSegmentMaxSize != p.conf.HLSSegmentMaxSize ||
		newConf.HLSDirectory != p.conf.HLSDirectory ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.HLSMuxerCloseAfter != p.conf.HLSMuxerCloseAfter ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closePathManager ||
		closeMetrics ||
		closeLogger

	closeWebRTCServer := newConf == nil ||
		newConf.WebRTC != p.conf.WebRTC ||
		newConf.WebRTCAddress != p.conf.WebRTCAddress ||
		newConf.WebRTCEncryption != p.conf.WebRTCEncryption ||
		newConf.WebRTCServerKey != p.conf.WebRTCServerKey ||
		newConf.WebRTCServerCert != p.conf.WebRTCServerCert ||
		!slices.Equal(newConf.WebRTCAllowOrigins, p.conf.WebRTCAllowOrigins) ||
		!reflect.DeepEqual(newConf.WebRTCTrustedProxies, p.conf.WebRTCTrustedProxies) ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.UDPReadBufferSize != p.conf.UDPReadBufferSize ||
		newConf.WebRTCLocalUDPAddress != p.conf.WebRTCLocalUDPAddress ||
		newConf.WebRTCLocalTCPAddress != p.conf.WebRTCLocalTCPAddress ||
		newConf.WebRTCIPsFromInterfaces != p.conf.WebRTCIPsFromInterfaces ||
		!reflect.DeepEqual(newConf.WebRTCIPsFromInterfacesList, p.conf.WebRTCIPsFromInterfacesList) ||
		!reflect.DeepEqual(newConf.WebRTCAdditionalHosts, p.conf.WebRTCAdditionalHosts) ||
		!reflect.DeepEqual(newConf.WebRTCICEServers2, p.conf.WebRTCICEServers2) ||
		newConf.WebRTCSTUNGatherTimeout != p.conf.WebRTCSTUNGatherTimeout ||
		newConf.WebRTCHandshakeTimeout != p.conf.WebRTCHandshakeTimeout ||
		newConf.WebRTCTrackGatherTimeout != p.conf.WebRTCTrackGatherTimeout ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closeMetrics ||
		closePathManager ||
		closeLogger

	closeSRTServer := newConf == nil ||
		newConf.SRT != p.conf.SRT ||
		newConf.SRTAddress != p.conf.SRTAddress ||
		newConf.RTSPAddress != p.conf.RTSPAddress ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.UDPMaxPayloadSize != p.conf.UDPMaxPayloadSize ||
		newConf.RunOnConnect != p.conf.RunOnConnect ||
		newConf.RunOnConnectRestart != p.conf.RunOnConnectRestart ||
		newConf.RunOnDisconnect != p.conf.RunOnDisconnect ||
		closePathManager ||
		closeLogger

	closeAPI := newConf == nil ||
		newConf.API != p.conf.API ||
		newConf.APIAddress != p.conf.APIAddress ||
		newConf.APIEncryption != p.conf.APIEncryption ||
		newConf.APIServerKey != p.conf.APIServerKey ||
		newConf.APIServerCert != p.conf.APIServerCert ||
		!slices.Equal(newConf.APIAllowOrigins, p.conf.APIAllowOrigins) ||
		!reflect.DeepEqual(newConf.APITrustedProxies, p.conf.APITrustedProxies) ||
		newConf.ReadTimeout != p.conf.ReadTimeout ||
		newConf.WriteTimeout != p.conf.WriteTimeout ||
		newConf.DumpPackets != p.conf.DumpPackets ||
		closeAuthManager ||
		closePathManager ||
		closeRTSPServer ||
		closeRTSPSServer ||
		closeRTMPServer ||
		closeHLSServer ||
		closeWebRTCServer ||
		closeSRTServer ||
		closeLogger

	if newConf == nil && p.confWatcher != nil {
		p.confWatcher.Close()
		p.confWatcher = nil
	}

	// mDNS only closes at full shutdown. Toggling mdns: on/off via
	// conf reload requires a restart for v1; revisit if customers
	// hit it.
	if newConf == nil && p.mdnsService != nil {
		p.mdnsService.Stop()
		p.mdnsService = nil
	}

	// TODO(phase6): close CRL / camera-sync / policy-sync / update
	// pollers here once their Phase 6 replacements land.

	if p.api != nil {
		if closeAPI {
			p.api.Close()
			p.api = nil
		} else if !calledByAPI { // avoid a loop
			p.api.ReloadConf(newConf)
		}
	}

	// ONVIF manager — close on full shutdown only. Subscriptions are
	// in-memory and don't survive restart by design (documented in
	// docs/web-ui.md as a future-slice item).
	if newConf == nil && p.onvifManager != nil {
		p.onvifManager.Close()
		p.onvifManager = nil
	}

	// Motion controller (Wave 4) — close on full shutdown only.
	// Per-camera cooldown timers are best-effort cancelled; in-flight
	// motion windows do not emit a synthetic motion_ended on
	// shutdown to avoid burying the operator in stop-time noise.
	if newConf == nil && p.motionController != nil {
		p.motionController.Close()
		p.motionController = nil
	}

	// Phase 6 foundation goroutines — cancel on full shutdown so
	// pathBridge / notifDispatcher / retentionMgr / cloudSvc /
	// mdnsRefresher / tlsReloader exit cleanly. fndCtx is shared
	// across all of them so a single cancel suffices.
	if newConf == nil && p.fndCtxCancel != nil {
		p.fndCtxCancel()
		p.fndCtxCancel = nil
		p.fndCtx = nil
		p.pathBridge = nil
		p.pmAdapter = nil
		p.notifDispatcher = nil
		p.retentionMgr = nil
		p.cloudSvc = nil
		p.tlsReloader = nil
		p.mdnsRefresher = nil
		p.camerasService = nil
		p.eventsService = nil
		p.scheduleResolver = nil
		p.camerasBus = nil
		p.eventsBus = nil
		p.credVault = nil
	}

	// LocalAuth store (pre-pairing auth slice 2026-05-06) — close on
	// full shutdown so the SQLite handle releases the WAL file.
	if newConf == nil && p.localAuthStore != nil {
		_ = p.localAuthStore.Close()
		p.localAuthStore = nil
		p.localAuth = nil
		p.auditEmit = nil
	}

	if closeSRTServer && p.srtServer != nil {
		p.srtServer.Close()
		p.srtServer = nil
	}

	if closeWebRTCServer && p.webRTCServer != nil {
		p.webRTCServer.Close()
		p.webRTCServer = nil
	}

	if closeHLSServer && p.hlsServer != nil {
		p.hlsServer.Close()
		p.hlsServer = nil
	}

	if closeRTMPSServer && p.rtmpsServer != nil {
		p.rtmpsServer.Close()
		p.rtmpsServer = nil
	}

	if closeRTMPServer && p.rtmpServer != nil {
		p.rtmpServer.Close()
		p.rtmpServer = nil
	}

	if closeRTSPSServer && p.rtspsServer != nil {
		p.rtspsServer.Close()
		p.rtspsServer = nil
	}

	if closeRTSPServer && p.rtspServer != nil {
		p.rtspServer.Close()
		p.rtspServer = nil
	}

	if closePathManager && p.pathManager != nil {
		p.pathManager.close()
		p.pathManager = nil
	}

	if closePlaybackServer && p.playbackServer != nil {
		p.playbackServer.Close()
		p.playbackServer = nil
	}

	if closeRecorderCleaner && p.recordCleaner != nil {
		p.recordCleaner.Close()
		p.recordCleaner = nil
	}

	if closePPROF && p.pprof != nil {
		p.pprof.Close()
		p.pprof = nil
	}

	if closeMetrics && p.metrics != nil {
		p.metrics.Close()
		p.metrics = nil
	}

	if closeAuthManager && p.authManager != nil {
		p.authManager = nil
	}

	if newConf == nil && p.externalCmdPool != nil {
		p.Log(logger.Info, "waiting for running hooks")
		p.externalCmdPool.Close()
	}

	if closeLogger && p.logger != nil {
		if newConf == nil {
			p.logger.Close()
		}
		p.logger = nil
	}
}

func (p *Core) reloadConf(newConf *conf.Conf, calledByAPI bool) error {
	oldLogger := p.logger

	p.closeResources(newConf, calledByAPI)

	p.conf = newConf

	err := p.createResources(false)
	if err != nil {
		p.logger = oldLogger
		return err
	}

	if p.logger != oldLogger {
		oldLogger.Close()
	}

	return nil
}

// APIConfigSet implements apiParent.
func (p *Core) APIConfigSet(conf *conf.Conf) {
	select {
	case p.chAPIConfigSet <- conf:
	case <-p.ctx.Done():
	}
}

// parseAPIPort extracts the port number from an APIAddress like
// "host:port" or ":port". Returns the recorder's default 9997 if
// parsing fails.
func parseAPIPort(addr string) int {
	if addr == "" {
		return 9997
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		_ = host
		return 9997
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return 9997
	}
	return p
}
