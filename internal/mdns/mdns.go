// Package mdns is the recorder's mDNS broadcaster. The recorder
// advertises _raikada-nvr._tcp.local so operator UIs and setup
// wizards on the same LAN can surface it for first-boot configuration.
//
// The advertise-only surface is a convenience: the cryptographic
// trust anchor for any future operator <-> recorder transport lives
// elsewhere (per pairing-flows when re-introduced). Operators can
// disable the entire surface via conf.MDNS.
package mdns

import (
	"context"
	"sync"

	"github.com/grandcat/zeroconf"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	// ServiceTypeNVR is the consumer NVR's mDNS service type. Operator
	// UIs browse for this to surface unconfigured / freshly-booted
	// recorders on the LAN.
	ServiceTypeNVR = "_raikada-nvr._tcp"

	announceInstancePrefix = "raikada-nvr-"
)

// Service runs the recorder's mDNS broadcaster. One instance per
// recorder process.
type Service struct {
	identity *identity.Identity
	logger   logger.Writer
	version  string
	port     int

	mu        sync.Mutex
	announcer *zeroconf.Server
	cancelFn  context.CancelFunc
	running   bool

	// txt is the latest TXT-record set Phase 6 will populate (e.g.
	// `setup=required` vs `setup=complete`). Defaults to a minimal
	// `version`+`id` set so the broadcaster is useful pre-Phase-6.
	txt map[string]string
}

// New constructs a Service. port is the recorder's HTTPS port (or
// the API port pre-pairing) — what gets announced in SRV records.
func New(id *identity.Identity, log logger.Writer, version string, port int) *Service {
	return &Service{
		identity: id,
		logger:   log,
		version:  version,
		port:     port,
		txt:      map[string]string{},
	}
}

// SetTXT replaces the TXT-record key/value set the broadcaster will
// announce. Phase 6 will call this with the runtime values that
// reflect the recorder's current setup state (e.g. `setup=required`,
// `setup=complete`). The next Refresh / Start picks up the new values.
func (s *Service) SetTXT(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]string, len(values))
	for k, v := range values {
		cp[k] = v
	}
	s.txt = cp
}

// Start kicks off the broadcaster. Non-blocking.
func (s *Service) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	_, cancel := context.WithCancel(context.Background())

	announcer, err := zeroconf.Register(
		announceInstancePrefix+s.identity.ID().String()[:8],
		ServiceTypeNVR,
		"local.",
		s.port,
		s.recorderTXTRecords(),
		nil,
	)
	if err != nil {
		cancel()
		return err
	}

	s.mu.Lock()
	s.running = true
	s.announcer = announcer
	s.cancelFn = cancel
	s.mu.Unlock()

	s.logger.Log(logger.Info, "[mdns] broadcasting %s port=%d id=%s",
		ServiceTypeNVR, s.port, s.identity.ID().String())
	return nil
}

// Stop halts the broadcaster.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancelFn
	announcer := s.announcer
	s.cancelFn = nil
	s.announcer = nil
	s.running = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if announcer != nil {
		announcer.Shutdown()
	}
}

// Refresh re-registers the broadcaster with current TXT records so
// state changes (e.g. setup=required → setup=complete after first-boot
// configuration) propagate to LAN listeners. zeroconf doesn't expose
// in-place TXT updates; the operational shape is shutdown + re-Register,
// which works fine for mDNS (clients re-resolve on next browse).
//
// Safe to call when the service isn't running (no-op).
func (s *Service) Refresh() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	oldAnnouncer := s.announcer
	s.announcer = nil
	s.mu.Unlock()

	if oldAnnouncer != nil {
		oldAnnouncer.Shutdown()
	}

	newAnnouncer, err := zeroconf.Register(
		announceInstancePrefix+s.identity.ID().String()[:8],
		ServiceTypeNVR,
		"local.",
		s.port,
		s.recorderTXTRecords(),
		nil,
	)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.announcer = newAnnouncer
	s.mu.Unlock()

	s.logger.Log(logger.Info, "[mdns] re-broadcasting %s id=%s",
		ServiceTypeNVR, s.identity.ID().String())
	return nil
}

// recorderTXTRecords builds the TXT record list. The mandatory
// fields are `version` and `id`; Phase 6 adds `setup=required|complete`
// + any other runtime-state fields via SetTXT.
func (s *Service) recorderTXTRecords() []string {
	s.mu.Lock()
	custom := make(map[string]string, len(s.txt))
	for k, v := range s.txt {
		custom[k] = v
	}
	s.mu.Unlock()

	out := []string{
		"version=" + s.version,
		"id=" + s.identity.ID().String(),
	}
	for k, v := range custom {
		if k == "version" || k == "id" {
			continue // mandatory fields take precedence over SetTXT overrides
		}
		out = append(out, k+"="+v)
	}
	return out
}
