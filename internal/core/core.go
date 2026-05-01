// Package core contains the main struct of the software.
package core

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/api"
	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/confwatcher"
	"github.com/bluenviron/mediamtx/internal/externalcmd"
	"github.com/bluenviron/mediamtx/internal/identity"
	mspairing "github.com/bluenviron/mediamtx/internal/pairing"
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
	identity        *identity.Identity
	pairingManager  *mspairing.Manager

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
		p.Log(logger.Info, "recorder identity loaded: id=%s dir=%s paired=%v",
			id.ID().String(), idDir, id.IsPaired())
	}

	if p.pairingManager == nil {
		p.pairingManager = mspairing.New(p.identity, p, string(version))
	}

	if p.authManager == nil {
		p.authManager = &auth.Manager{
			Method:             p.conf.AuthMethod,
			InternalUsers:      p.conf.AuthInternalUsers,
			HTTPAddress:        p.conf.AuthHTTPAddress,
			HTTPFingerprint:    p.conf.AuthHTTPFingerprint,
			HTTPExclude:        p.conf.AuthHTTPExclude,
			JWTJWKS:            p.conf.AuthJWTJWKS,
			JWTJWKSFingerprint: p.conf.AuthJWTJWKSFingerprint,
			JWTClaimKey:        p.conf.AuthJWTClaimKey,
			JWTExclude:         p.conf.AuthJWTExclude,
			JWTInHTTPQuery:     p.conf.AuthJWTInHTTPQuery,
			JWTIssuer:          p.conf.AuthJWTIssuer,
			JWTAudience:        p.conf.AuthJWTAudience,
			ReadTimeout:        time.Duration(p.conf.ReadTimeout),
		}
	}

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
		i := &api.API{
			Version:        string(version),
			Started:        started,
			Address:        p.conf.APIAddress,
			DumpPackets:    p.conf.DumpPackets,
			Encryption:     p.conf.APIEncryption,
			ServerKey:      p.conf.APIServerKey,
			ServerCert:     p.conf.APIServerCert,
			AllowOrigins:   p.conf.APIAllowOrigins,
			TrustedProxies: p.conf.APITrustedProxies,
			ReadTimeout:    p.conf.ReadTimeout,
			WriteTimeout:   p.conf.WriteTimeout,
			Conf:           p.conf,
			AuthManager:    p.authManager,
			Identity:       p.identity,
			Pairing:        p.pairingManager,
			PathManager:    p.pathManager,
			RTSPServer:     p.rtspServer,
			RTSPSServer:    p.rtspsServer,
			RTMPServer:     p.rtmpServer,
			RTMPSServer:    p.rtmpsServer,
			HLSServer:      p.hlsServer,
			WebRTCServer:   p.webRTCServer,
			SRTServer:      p.srtServer,
			Parent:         p,
		}
		err = i.Initialize()
		if err != nil {
			return err
		}
		p.api = i

		// Wire the pipeline-side Event publish target so path.go's
		// camera.online / camera.offline emitters land in the same
		// EventStore that /v1/events serves from. Tenant id is
		// captured by value (not by closure over p.conf) to avoid a
		// data race with reloadConf at core.go:1089. createResources
		// runs on both initial setup and every conf reload, so each
		// reload re-snapshots tenantID through this same SetPipeline-
		// EventTarget call — legitimate tenant-rebind paths still
		// flow through.
		var tenantID string
		if p.conf != nil {
			tenantID = p.conf.TenantID
		}
		api.SetPipelineEventTarget(api.DefaultEventStore(), func() string {
			return tenantID
		})

		// Wire the recorder-side segment.write_failed publisher to
		// land emissions in the same EventStore as the rest of the
		// pipeline-side kinds. recorder_instance.run() invokes the
		// hook only when errors.As detects a *SegmentWriteError, so
		// non-write failures (network, codec) do not surface as a
		// canonical write-failed event.
		recorder.SetSegmentWriteFailedPublisher(api.PublishSegmentWriteFailed)
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
		newConf.AuthJWTJWKS != p.conf.AuthJWTJWKS ||
		newConf.AuthJWTJWKSFingerprint != p.conf.AuthJWTJWKSFingerprint ||
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

	if p.api != nil {
		if closeAPI {
			p.api.Close()
			p.api = nil
		} else if !calledByAPI { // avoid a loop
			p.api.ReloadConf(newConf)
		}
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
