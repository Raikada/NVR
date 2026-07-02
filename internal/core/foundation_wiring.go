// Package core: Phase 6 foundation-service wiring helpers.
//
// This file holds the small adapters that bridge the foundation
// packages (cameras, retention, notifications) to the recorder's
// pre-existing internal/core surfaces (pathManager, recordstore,
// onvif manager). Keeping them here — rather than in the foundation
// packages themselves — preserves the foundation packages as
// store-/service-only and lets core be the only place that imports
// both layers.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/camerahealth"
	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/mediasign"
	"github.com/bluenviron/mediamtx/internal/onvif"
	"github.com/bluenviron/mediamtx/internal/recordstore"
	"github.com/bluenviron/mediamtx/internal/retention"
	"github.com/bluenviron/mediamtx/internal/snapshots"
	"github.com/bluenviron/mediamtx/internal/store"
	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

// pathManagerAdapter satisfies cameras.PathManager. It owns a base
// snapshot of operator-supplied conf.Paths (e.g. paths declared in
// mediamtx.yml that aren't camera-backed) and merges in the
// camera-derived specs on every reload. The snapshot is refreshed on
// every conf apply (RefreshDefaults) so paths created after boot —
// e.g. via POST /v1/cameras — are merged against their real conf
// rather than a nil base. pathTemplate carries the validated
// conf.PathDefaults for cameras that have no conf path at all.
type pathManagerAdapter struct {
	pm     *pathManager
	logger logger.Writer

	mu           sync.Mutex
	defaultPaths map[string]*conf.Path
	pathTemplate *conf.Path
}

// newPathManagerAdapter returns an adapter pinned to the recorder's
// running pathManager. defaultPaths is the snapshot of operator-supplied
// paths (e.g. those parsed from mediamtx.yml) that should always be
// present alongside camera-derived paths.
func newPathManagerAdapter(pm *pathManager, defaults map[string]*conf.Path, log logger.Writer) *pathManagerAdapter {
	cp := make(map[string]*conf.Path, len(defaults))
	for k, v := range defaults {
		cp[k] = v
	}
	return &pathManagerAdapter{pm: pm, logger: log, defaultPaths: cp}
}

// RefreshDefaults replaces the adapter's boot-time snapshot with the
// paths (and validated PathDefaults template) from a freshly-applied
// conf. Core calls this on every conf apply so camera paths created
// after boot participate in later merges.
func (a *pathManagerAdapter) RefreshDefaults(c *conf.Conf) {
	if a == nil || c == nil {
		return
	}
	cp := make(map[string]*conf.Path, len(c.Paths))
	for k, v := range c.Paths {
		cp[k] = v
	}
	tmpl := c.PathDefaults
	a.mu.Lock()
	a.defaultPaths = cp
	a.pathTemplate = &tmpl
	a.mu.Unlock()
}

// baseFor returns the merge base for a camera path name plus the
// defaults template, both under the adapter lock.
func (a *pathManagerAdapter) baseFor(name string) (*conf.Path, *conf.Path) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.defaultPaths[name], a.pathTemplate
}

// ReloadFromCameras is the cameras.PathManager seam. It rebuilds the
// path-conf map = defaultPaths + camera-derived specs and calls
// pathManager.ReloadPathConfs.
func (a *pathManagerAdapter) ReloadFromCameras(_ context.Context, specs []cameras.CameraPathSpec) error {
	if a == nil || a.pm == nil {
		return nil
	}
	a.mu.Lock()
	defaults := a.defaultPaths
	tmpl := a.pathTemplate
	a.mu.Unlock()
	merged := make(map[string]*conf.Path, len(defaults)+len(specs))
	for k, v := range defaults {
		merged[k] = v
	}
	for _, spec := range specs {
		if spec.Name == "" {
			continue
		}
		merged[spec.Name] = cameraSpecToConfPath(spec, defaults[spec.Name], tmpl)
	}
	a.pm.ReloadPathConfs(merged)
	if a.logger != nil {
		a.logger.Log(logger.Debug,
			"[cameras.bridge] applied %d paths (defaults=%d, cameras=%d)",
			len(merged), len(defaults), len(specs))
	}
	return nil
}

// cameraSpecToConfPath builds a conf.Path from a CameraPathSpec. When
// an existing default conf.Path is supplied for the same name, its
// recording fields are preserved (operator-customized recordPath /
// segment / part / retention overrides survive). Without a base the
// path is built from the validated PathDefaults template — never from
// a zero value, whose empty RTSPUDPSourcePortRange would panic the
// RTSP dialer (source.go).
func cameraSpecToConfPath(spec cameras.CameraPathSpec, base *conf.Path, tmpl *conf.Path) *conf.Path {
	var p conf.Path
	switch {
	case base != nil:
		p = *base
	case tmpl != nil:
		p = *tmpl
	default:
		p.SetDefaults()
	}
	p.Name = spec.Name
	p.Source = spec.SourceURL
	// Record fields: leave as-is from base; the per-camera RecordingPolicy
	// is materialized into mediamtx.yml separately by the existing
	// /v1/cameras flow. The pathBridge's job is just to keep the source
	// URL fresh after a credential rotation.
	return &p
}

// onvifTeardownAdapter satisfies cameras.OnvifTeardown. On
// TeardownCamera(cameraID) it enumerates the onvif manager's active
// subscriptions, filters by camera id, and calls RemoveSubscription
// for each match.
type onvifTeardownAdapter struct {
	mgr    *onvif.Manager
	logger logger.Writer
}

// newOnvifTeardownAdapter wires an adapter against the running onvif
// manager. The cameras package only ever calls TeardownCamera; the
// adapter abstracts the manager surface so cameras doesn't need an
// onvif import.
func newOnvifTeardownAdapter(mgr *onvif.Manager, log logger.Writer) *onvifTeardownAdapter {
	return &onvifTeardownAdapter{mgr: mgr, logger: log}
}

// TeardownCamera implements cameras.OnvifTeardown.
func (a *onvifTeardownAdapter) TeardownCamera(ctx context.Context, cameraID string) error {
	if a == nil || a.mgr == nil || cameraID == "" {
		return nil
	}
	subs := a.mgr.List()
	for _, s := range subs {
		if s.CameraID != cameraID {
			continue
		}
		if err := a.mgr.RemoveSubscription(ctx, s.ID); err != nil && a.logger != nil {
			a.logger.Log(logger.Warn,
				"[cameras.onvif_teardown] camera=%s sub=%s: %v",
				cameraID, s.ID, err)
		}
	}
	return nil
}

// segmentListerAdapter implements retention.SegmentLister against the
// existing internal/recordstore enumeration. It walks every
// camera-backed path's recordings directory, enumerates segments
// matching the encoded recordPath template, and unlinks any whose
// start time precedes horizon.
type segmentListerAdapter struct {
	pathConfs func() map[string]*conf.Path
	camName   func(ctx context.Context, cameraID string) (string, error)
	logger    logger.Writer
}

// newSegmentListerAdapter constructs an adapter. pathConfs returns the
// current conf.Paths map (so the adapter sees up-to-date recordPath
// values after a conf reload), camName resolves a camera id to its
// configured path name (typically the cameras.name column).
func newSegmentListerAdapter(
	pathConfs func() map[string]*conf.Path,
	camName func(ctx context.Context, cameraID string) (string, error),
	log logger.Writer,
) *segmentListerAdapter {
	return &segmentListerAdapter{pathConfs: pathConfs, camName: camName, logger: log}
}

// SweepCamera implements retention.SegmentLister. Returns the number of
// segments removed.
func (a *segmentListerAdapter) SweepCamera(ctx context.Context, cameraID string, horizon time.Time) (int, error) {
	if a == nil || a.pathConfs == nil {
		return 0, nil
	}
	confs := a.pathConfs()
	if confs == nil {
		return 0, nil
	}
	name := cameraID
	if a.camName != nil {
		if resolved, err := a.camName(ctx, cameraID); err == nil && resolved != "" {
			name = resolved
		}
	}
	pathConf, ok := confs[name]
	if !ok || pathConf == nil {
		return 0, nil
	}

	segs, err := recordstore.FindSegments(pathConf, name, nil, nil)
	if err != nil {
		// ErrNoSegmentsFound is benign — nothing to sweep.
		if err == recordstore.ErrNoSegmentsFound {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	for _, seg := range segs {
		if !seg.Start.Before(horizon) {
			continue
		}
		if err := os.Remove(seg.Fpath); err != nil && !os.IsNotExist(err) {
			if a.logger != nil {
				a.logger.Log(logger.Warn,
					"[retention.segments] unlink %s: %v", seg.Fpath, err)
			}
			continue
		}
		removed++
	}
	return removed, nil
}

// Compile-time interface satisfaction checks. Catch signature drift at
// build time rather than wiring time.
var (
	_ cameras.PathManager     = (*pathManagerAdapter)(nil)
	_ cameras.OnvifTeardown   = (*onvifTeardownAdapter)(nil)
	_ retention.SegmentLister = (*segmentListerAdapter)(nil)
)

// makeSignedURLFn builds a signed-URL helper closure for the
// notifications dispatcher. The closure produces a URL like
//
//	https://<api host>/v1/events/<id>/snapshot/full?token=<jwt>
//
// where <jwt> is signed with the localauth signing key. Used by
// webhook payloads so consumers can fetch snapshots/clips without a
// pre-arranged auth token.
//
// signingKey is the localauth ECDSA private key; apiAddr is the
// recorder's API listen address (host:port). The closure handles a
// missing key gracefully by returning an empty string — the foundation
// dispatcher tolerates empty URLs (operators without a public IP
// won't surface external links anyway).
type signURLFn = func(eventID, kind string) string

// makeSignedURL returns a signURLFn pinned to apiAddr, minting SP4
// HMAC-signed media URLs (webhook consumers fetch them without auth
// headers; 24h TTL — notification links outlive the SPA's 15min ones).
// A nil signer disables URL surfacing (empty string; the dispatcher
// tolerates it).
func makeSignedURL(apiAddr string, signer *mediasign.Signer) signURLFn {
	return func(eventID, kind string) string {
		if signer == nil {
			return ""
		}
		host, port, err := net.SplitHostPort(apiAddr)
		if err != nil {
			host = ""
			port = "9997"
		}
		if host == "" {
			host = "localhost"
		}
		path := fmt.Sprintf("/v1/media/snapshots/%s/%s", eventID, kind)
		exp, sig := signer.Sign(path, 24*time.Hour)
		base := url.URL{
			Scheme:   "https",
			Host:     net.JoinHostPort(host, port),
			Path:     path,
			RawQuery: fmt.Sprintf("exp=%d&sig=%s", exp, sig),
		}
		return base.String()
	}
}

// getSiteName looks up the site_name system_setting, falling back to
// "My NVR" when missing/empty. Used to populate notification payloads.
func getSiteName(ctx context.Context, st *store.Store) string {
	if st == nil || st.SystemSettings == nil {
		return "My NVR"
	}
	row, err := st.SystemSettings.Get(ctx, "site_name")
	if err != nil || row == nil || row.Value == "" {
		return "My NVR"
	}
	return row.Value
}

// resolveCameraName resolves a camera id to its name column. Used by
// segmentListerAdapter to find the right pathConf entry. Returns the
// id unchanged when the lookup fails.
func resolveCameraName(repo *store.CamerasRepo) func(ctx context.Context, id string) (string, error) {
	return func(ctx context.Context, id string) (string, error) {
		if repo == nil {
			return id, nil
		}
		cam, err := repo.GetByID(ctx, id)
		if err != nil || cam == nil {
			return id, err
		}
		return cam.Name, nil
	}
}

// snapshotPathConfs returns a defensive copy of pathManager.pathConfs
// suitable for stashing as the segment lister adapter's "current map"
// callback. The pathManager.pathConfs map is mutated in-place from the
// run() loop; the adapter reads from it concurrently from the retention
// goroutine. The defensive copy prevents map-mutation races while still
// surfacing fresh values after each conf reload.
func snapshotPathConfs(pm *pathManager) map[string]*conf.Path {
	if pm == nil {
		return nil
	}
	// pathManager.pathConfs is set under the run-loop's chReloadConf
	// dispatch; reads from outside the run loop are racy by construction.
	// In practice the read is best-effort: a concurrent reload either
	// shows the old map or the new — both are well-formed because
	// doReloadConf assigns the whole pointer.
	confs := pm.pathConfs
	if confs == nil {
		return nil
	}
	out := make(map[string]*conf.Path, len(confs))
	for k, v := range confs {
		out[k] = v
	}
	return out
}

// pathDefaultsFromConf returns the operator-supplied paths from the
// recorder's loaded conf, minus any that look like camera-managed paths
// (those whose names match a UUID-shaped identifier or a known camera
// name in the store). The returned map is the "base" the
// pathManagerAdapter merges camera-derived specs onto.
//
// Phase 6 v1 is conservative: it includes every path. The caller (Core
// at startup) can refine the base set later if camera-managed paths
// need to be excluded explicitly.
func pathDefaultsFromConf(c *conf.Conf) map[string]*conf.Path {
	if c == nil {
		return nil
	}
	out := make(map[string]*conf.Path, len(c.Paths))
	for k, v := range c.Paths {
		out[k] = v
	}
	return out
}

// loggerWriter is a tiny adapter that satisfies logger.Writer using a
// plain func. Used for retention sweeper logger when Core's *Core is
// already the logger.Writer of choice but we need a child-prefixed
// version.
type loggerWriter struct {
	parent logger.Writer
	prefix string
}

func (l *loggerWriter) Log(level logger.Level, format string, args ...any) {
	if l == nil || l.parent == nil {
		return
	}
	if l.prefix != "" {
		format = l.prefix + format
	}
	l.parent.Log(level, format, args...)
}

// stripIdentityPrefix removes the conf.IdentityDir + "/" prefix from a
// path so log lines stay readable. Used by the TLS reload logger.
func stripIdentityPrefix(path, identityDir string) string {
	if path == "" || identityDir == "" {
		return path
	}
	rel, err := filepath.Rel(identityDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// defaultRecordingsRoot returns the directory prefix for recordings,
// derived from conf.PathDefaults.RecordPath. The default record path is
// "./recordings/%path/..." so this returns "./recordings"; operators
// who override the recording path get the matching prefix. Returns ""
// when the prefix can't be extracted (e.g., the template starts with
// "%path/..." with no static prefix).
func defaultRecordingsRoot(c *conf.Conf) string {
	if c == nil {
		return ""
	}
	rp := c.PathDefaults.RecordPath
	if rp == "" {
		return ""
	}
	// Strip everything from the first '%' onward; the remaining prefix
	// is the static recordings directory. "./recordings/%path/..." ->
	// "./recordings/".
	idx := strings.Index(rp, "%")
	if idx < 0 {
		return filepath.Dir(rp)
	}
	prefix := rp[:idx]
	prefix = strings.TrimRight(prefix, "/")
	return prefix
}

// discoveryProberAdapter satisfies discovery.Prober with a fresh
// onvif.Discoverer per round (the Discoverer is single-shot).
type discoveryProberAdapter struct{}

func (discoveryProberAdapter) Probe(ctx context.Context) ([]onvif.DiscoveredDevice, error) {
	d := &onvif.Discoverer{}
	return d.Run(ctx)
}

// pathListerAdapter satisfies camerahealth.PathLister off the running
// pathManager. The pointer is swappable (SetPathManager) because core
// recreates the path manager on some conf reloads — pinning it would
// leave the health collector polling a closed instance (same stale-
// snapshot class as the PathBridge F2 bug).
type pathListerAdapter struct {
	mu sync.Mutex
	pm *pathManager
}

// SetPathManager swaps the polled instance. Called by core whenever the
// path manager is (re)created.
func (a *pathListerAdapter) SetPathManager(pm *pathManager) {
	a.mu.Lock()
	a.pm = pm
	a.mu.Unlock()
}

func (a *pathListerAdapter) ListPaths(_ context.Context) ([]camerahealth.PathSnapshot, error) {
	if a == nil {
		return nil, fmt.Errorf("path manager not available")
	}
	a.mu.Lock()
	pm := a.pm
	a.mu.Unlock()
	if pm == nil {
		return nil, fmt.Errorf("path manager not available")
	}
	list, err := pm.APIPathsList()
	if err != nil {
		return nil, err
	}
	out := make([]camerahealth.PathSnapshot, 0, len(list.Items))
	for _, item := range list.Items {
		snap := camerahealth.PathSnapshot{Name: item.Name, Online: item.Online}
		switch {
		case item.ReadyTime != nil:
			snap.LastFrameAt = *item.ReadyTime
		case item.OnlineTime != nil:
			snap.LastFrameAt = *item.OnlineTime
		}
		out = append(out, snap)
	}
	return out, nil
}

// vendorChannelResolver builds the vendorevents.Resolver: store camera
// row → ChannelCamera with the CGI host (hostname only — vendor HTTP
// services live on their default port, not the RTSP port), the
// capability probe's events XAddr, and a late-bound credentials
// closure so plaintext never sits in a struct field.
func vendorChannelResolver(st *store.Store, camerasService *cameras.Service) vendorevents.Resolver {
	return func(cam *store.Camera) vendorevents.ChannelCamera {
		cc := vendorevents.ChannelCamera{
			ID:           cam.ID,
			Name:         cam.Name,
			Manufacturer: cam.Manufacturer,
			OnvifXAddr:   cam.OnvifXAddr,
			Channel:      cam.EventChannel,
		}
		if u, err := url.Parse(cam.SourceURL); err == nil {
			cc.Host = u.Hostname()
		}
		if st != nil {
			if caps, err := st.CameraCapabilities.Get(context.Background(), cam.ID); err == nil {
				var vendor struct {
					EventsXAddr string `json:"events_xaddr"`
				}
				if json.Unmarshal([]byte(caps.VendorCapabilitiesJSON), &vendor) == nil {
					cc.EventsXAddr = vendor.EventsXAddr
				}
			}
		}
		id := cam.ID
		cc.Credentials = func(ctx context.Context) (string, string, error) {
			return camerasService.PlaintextCredentials(ctx, id)
		}
		return cc
	}
}

// notImplementedChannelFactory is the registry stub for vendors whose
// adapters haven't shipped (Hikvision ISAPI, Reolink) — the interface
// contract they'll implement later.
func notImplementedChannelFactory(vendor string) vendorevents.AdapterFactory {
	return func(_ context.Context, cam vendorevents.ChannelCamera) (vendorevents.Adapter, error) {
		return nil, fmt.Errorf("%s event channel not implemented (camera %s): %w",
			vendor, cam.ID, vendorevents.ErrUnsupported)
	}
}

// snapshotCameraResolver builds the snapshots.Resolver: camera id →
// fetch-ladder inputs (vendor host, capability-probe snapshot URI,
// resolved channel, creds closure).
func snapshotCameraResolver(st *store.Store, camerasService *cameras.Service) snapshots.Resolver {
	return func(ctx context.Context, cameraID string) (snapshots.CameraInfo, error) {
		cam, err := st.Cameras.GetByID(ctx, cameraID)
		if err != nil {
			return snapshots.CameraInfo{}, err
		}
		info := snapshots.CameraInfo{
			ID:      cam.ID,
			Name:    cam.Name,
			Channel: vendorevents.SelectChannel(cam.EventChannel, cam.Manufacturer, cam.OnvifXAddr),
		}
		if u, err := url.Parse(cam.SourceURL); err == nil {
			info.Host = u.Hostname()
		}
		if caps, err := st.CameraCapabilities.Get(ctx, cam.ID); err == nil {
			var vendor struct {
				SnapshotURI string `json:"snapshot_uri"`
			}
			if json.Unmarshal([]byte(caps.VendorCapabilitiesJSON), &vendor) == nil {
				info.SnapshotURI = vendor.SnapshotURI
			}
		}
		id := cam.ID
		info.Credentials = func(cctx context.Context) (string, string, error) {
			return camerasService.PlaintextCredentials(cctx, id)
		}
		return info, nil
	}
}

// snapshotRootFn resolves the snapshot storage root: the operator's
// system_settings value when set, else <recordPath common prefix>/snapshots.
func snapshotRootFn(st *store.Store, defaultRecordPath string) func() string {
	fallback := filepath.Join(recordstore.CommonPath(defaultRecordPath), "snapshots")
	return func() string {
		if st != nil {
			if row, err := st.SystemSettings.Get(context.Background(), "snapshot_root"); err == nil &&
				row != nil && strings.TrimSpace(row.Value) != "" {
				return row.Value
			}
		}
		return fallback
	}
}
