// Package mdns is the recorder's mDNS broadcaster + listener per
// pairing API contract §8.3-8.4. The recorder broadcasts
// _raikada-recorder._tcp.local so an MS on the same LAN can surface
// it for operator-driven pairing, and listens for
// _raikada-management._tcp.local advertisements so the recorder's
// setup wizard can pre-fill discovered MS URLs.
//
// mDNS does not establish trust — it's a convenience layer per
// pairing-flows.md §2.2 and ADR 0012 D5 ("the cryptographic pinning
// via QR pairing-token is the trust anchor"). Operators can disable
// the entire surface via conf.MDNS.
package mdns

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/bluenviron/mediamtx/internal/identity"
	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	ServiceTypeRecorder   = "_raikada-recorder._tcp"
	ServiceTypeManagement = "_raikada-management._tcp"

	announceInstancePrefix = "raikada-recorder-"
	listenerStaleAfter     = 5 * time.Minute
)

// DiscoveredManagement is one entry in the recorder's mDNS-listener
// cache.
type DiscoveredManagement struct {
	MSID         string    `json:"ms_id,omitempty"`
	Hostname     string    `json:"hostname"`
	Addresses    []string  `json:"addresses"`
	Version      string    `json:"version,omitempty"`
	TenantID     string    `json:"tenant_id,omitempty"`
	Port         int       `json:"port"`
	URL          string    `json:"url"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// Service runs the recorder's mDNS broadcaster + listener. One
// instance per recorder process.
type Service struct {
	identity *identity.Identity
	logger   logger.Writer
	version  string
	port     int

	mu        sync.Mutex
	announcer *zeroconf.Server
	cancelFn  context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	cache     map[string]*DiscoveredManagement // keyed by ms_id (or hostname when ms_id absent)
}

// New constructs a Service. port is the recorder's HTTPS port (or
// the API port pre-pairing) — what gets announced in SRV records.
func New(id *identity.Identity, log logger.Writer, version string, port int) *Service {
	return &Service{
		identity: id,
		logger:   log,
		version:  version,
		port:     port,
		cache:    make(map[string]*DiscoveredManagement),
	}
}

// Start kicks off broadcaster + listener goroutines. Non-blocking.
func (s *Service) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	announcer, err := zeroconf.Register(
		announceInstancePrefix+s.identity.ID().String()[:8],
		ServiceTypeRecorder,
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

	s.logger.Log(logger.Info, "[mdns] broadcasting %s port=%d id=%s paired=%v",
		ServiceTypeRecorder, s.port, s.identity.ID().String(), s.identity.IsPaired())

	s.wg.Add(2)
	go s.runListener(ctx)
	go s.runSweeper(ctx)
	return nil
}

// Stop halts broadcaster + listener.
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
	s.wg.Wait()
}

// Discovered returns a snapshot of currently-cached MS advertisements.
func (s *Service) Discovered() []DiscoveredManagement {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DiscoveredManagement, 0, len(s.cache))
	for _, d := range s.cache {
		out = append(out, *d)
	}
	return out
}

// recorderTXTRecords builds the TXT record list per pairing API
// contract §8.3.
func (s *Service) recorderTXTRecords() []string {
	paired := "false"
	if s.identity.IsPaired() {
		paired = "true"
	}
	return []string{
		"version=" + s.version,
		"id=" + s.identity.ID().String(),
		"paired=" + paired,
	}
}

// runListener browses for management advertisements and updates the
// cache. The browse cycle re-runs on a 30-second cadence so newly-
// announced MS instances surface within roughly that window.
func (s *Service) runListener(ctx context.Context) {
	defer s.wg.Done()
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		s.logger.Log(logger.Warn, "[mdns] resolver: %s", err)
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		entries := make(chan *zeroconf.ServiceEntry, 16)
		browseCtx, browseCancel := context.WithTimeout(ctx, 30*time.Second)
		go s.consume(ctx, entries)
		if err := resolver.Browse(browseCtx, ServiceTypeManagement, "local.", entries); err != nil {
			s.logger.Log(logger.Warn, "[mdns] browse: %s", err)
		}
		<-browseCtx.Done()
		browseCancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *Service) consume(ctx context.Context, entries <-chan *zeroconf.ServiceEntry) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			s.handleEntry(entry)
		}
	}
}

func (s *Service) handleEntry(entry *zeroconf.ServiceEntry) {
	if entry == nil {
		return
	}
	txt := parseTXT(entry.Text)
	addrs := []string{}
	for _, ip := range entry.AddrIPv4 {
		addrs = append(addrs, ip.String())
	}
	for _, ip := range entry.AddrIPv6 {
		addrs = append(addrs, ip.String())
	}

	key := txt["ms_id"]
	if key == "" {
		key = entry.HostName
	}
	url := ""
	if len(addrs) > 0 {
		url = "https://" + addrs[0] + ":" + itoa(entry.Port)
	}
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.cache[key]
	if !ok {
		s.cache[key] = &DiscoveredManagement{
			MSID:         txt["ms_id"],
			Hostname:     entry.HostName,
			Addresses:    addrs,
			Version:      txt["version"],
			TenantID:     txt["tenant_id"],
			Port:         entry.Port,
			URL:          url,
			FirstSeenAt:  now,
			LastSeenAt:   now,
		}
		return
	}
	existing.MSID = txt["ms_id"]
	existing.Hostname = entry.HostName
	existing.Addresses = addrs
	existing.Version = txt["version"]
	existing.TenantID = txt["tenant_id"]
	existing.Port = entry.Port
	existing.URL = url
	existing.LastSeenAt = now
}

func (s *Service) runSweeper(ctx context.Context) {
	defer s.wg.Done()
	tick := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cutoff := time.Now().Add(-listenerStaleAfter)
			s.mu.Lock()
			for k, d := range s.cache {
				if d.LastSeenAt.Before(cutoff) {
					delete(s.cache, k)
				}
			}
			s.mu.Unlock()
		}
	}
}

func parseTXT(records []string) map[string]string {
	out := make(map[string]string, len(records))
	for _, r := range records {
		idx := strings.Index(r, "=")
		if idx <= 0 {
			continue
		}
		out[r[:idx]] = r[idx+1:]
	}
	return out
}

// itoa is a tiny int→string without strconv import; keeps the file
// dep list minimal. (zeroconf entry.Port is int.)
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 11)
	for n > 0 {
		buf = append([]byte{byte(n%10) + '0'}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
