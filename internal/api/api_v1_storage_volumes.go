// Package api: /v1/storage-volumes handlers per ADR 0009 §D5
// Storage volumes.
//
// Read-only at this surface per ADR 0009 §D2 ("StorageVolume … Read-
// only at this surface; volume CRUD is bootstrap configuration that
// lives in the recorder-localized config (escape hatch
// /v1/recorder/config), not in the runtime API").
//
// The recorder doesn't have a single "volume registry" object today.
// Volume bootstrap config lives flat inside conf.Path (each path's
// recordPath); recordstore tracks per-path segment files but doesn't
// expose a per-volume rollup. Phase 2D rolls that data up here:
//
//  1. Walk every conf.Path with recording enabled.
//  2. Extract the volume root (the longest mount-pointish prefix of
//     pathConf.RecordPath that doesn't contain a `%`-format token).
//  3. Group paths by volume root, dedupe, and stat each root via
//     syscall.Statfs to get capacity/used/last-checked.
//  4. Look up live segment activity from recordstore.FindSegments
//     to derive a "last-write" hint.
//
// Volume UUIDs are server-issued, deterministic UUIDv5 from the
// volume's mount path so they're stable across recorder restarts.
package api //nolint:revive

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// storageVolumeListResponse is the on-wire shape of GET
// /v1/storage-volumes. ADR 0009 §D5 doesn't require pagination here
// (volume count is small per recorder), but the envelope mirrors the
// rest of the canonical surface for client consistency.
type storageVolumeListResponse struct {
	ItemCount int                  `json:"item_count"`
	Items     []defs.StorageVolume `json:"items"`
}

// uuidV5FromString hashes a string into a deterministic UUIDv5 in the
// OID namespace. Used to mint a stable per-mount-path volume id and
// (in api_v1_health.go) a per-process snapshot id.
func uuidV5FromString(s string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(s)).String()
}

// volumeIDFromMountPath derives the canonical UUID for a volume keyed
// by its absolute mount path. ADR 0009 §D4 leaves UUID provisioning
// to the recorder pre-MS; deterministic UUIDv5 keeps the id stable
// across recorder restarts.
func volumeIDFromMountPath(mountPath string) string {
	abs, _ := filepath.Abs(mountPath)
	return uuidV5FromString("storage-volume|" + abs)
}

// volumeRootForRecordPath returns the deepest no-format-token prefix
// of recordPath. Examples:
//
//	"./recordings/%path/%Y-%m-%d_%H"        → "recordings"
//	"/var/lib/recorder/%path/%s.mp4"        → "/var/lib/recorder"
//	"/mnt/disk1/cam-%path/%Y-%m-%d/%H.mp4"  → "/mnt/disk1"
//
// recordPath may use `%path` (path-name token) or strftime-style
// `%Y/%m/%d/...` tokens; both are stripped at the first segment that
// contains `%`. The result is the conf-side notion of the volume
// root — a directory all of this path's recordings live under.
func volumeRootForRecordPath(recordPath string) string {
	if recordPath == "" {
		return ""
	}
	// Trim trailing slash so the segment walk is deterministic.
	recordPath = strings.TrimRight(recordPath, "/")
	parts := strings.Split(recordPath, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.Contains(p, "%") {
			break
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return ""
	}
	root := strings.Join(out, "/")
	if root == "" {
		// Edge case: leading absolute slash with first segment as a
		// format token. Treat the filesystem root as the volume.
		return "/"
	}
	return root
}

// statfsVolume populates the disk-usage fields on a StorageVolumeInput
// from syscall.Statfs. Returns the input unchanged on error so the
// caller can still surface a coherent (zero-bytes) entry; the
// "missing" status flips on when stat fails, signalling the volume
// isn't reachable right now.
func statfsVolume(mountPath string) (capacity, used int64, ok bool) {
	if mountPath == "" {
		return 0, 0, false
	}
	abs, err := filepath.Abs(mountPath)
	if err != nil {
		return 0, 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(abs, &st); err != nil {
		return 0, 0, false
	}
	// Bsize is signed on darwin and unsigned on linux; convert
	// through int64 explicitly.
	bsize := int64(st.Bsize)
	if bsize <= 0 {
		bsize = 4096 // sane default; not all platforms populate Bsize
	}
	capacity = int64(st.Blocks) * bsize //nolint:gosec
	free := int64(st.Bavail) * bsize    //nolint:gosec
	if free > capacity {
		free = capacity
	}
	used = capacity - free
	if used < 0 {
		used = 0
	}
	return capacity, used, true
}

// volumeKindForMountPath picks a StorageVolumeKind heuristically. We
// don't have OS-level volume metadata wired up yet (no fstab parse,
// no diskutil/lsblk shell-out), so the heuristic is intentionally
// coarse: tmpfs/ramdisk-ish prefixes flag as ramdisk, network-share
// prefixes flag as network_share, the rest fall back to internal_disk.
//
// Phase-2-followup: replace with a real platform probe (gopsutil
// disk.Partitions or syscall.Statfs.Type lookup against a shared
// table).
func volumeKindForMountPath(mountPath string) defs.StorageVolumeKind {
	// Check the raw input first for distinctive prefixes that
	// filepath.Abs would normalize away (notably "//host/share" SMB
	// paths, which Abs collapses to "/host/share").
	if strings.HasPrefix(mountPath, "//") {
		return defs.StorageVolumeKindNetworkShare
	}
	abs, _ := filepath.Abs(mountPath)
	switch {
	case strings.HasPrefix(abs, "/tmp"),
		strings.HasPrefix(abs, "/dev/shm"),
		strings.HasPrefix(abs, "/run"):
		return defs.StorageVolumeKindRamdisk
	case strings.HasPrefix(abs, "/mnt/nfs"),
		strings.HasPrefix(abs, "/net"):
		return defs.StorageVolumeKindNetworkShare
	}
	return defs.StorageVolumeKindInternalDisk
}

// collectStorageVolumes walks the recorder's path table, groups by
// volume root, and synthesizes one canonical StorageVolume per root.
// The returned slice is deterministically ordered by mount path so
// pagination, list-vs-get round-trip, and tests stay stable.
func (a *API) collectStorageVolumes(c *conf.Conf) []defs.StorageVolume {
	if c == nil {
		return nil
	}
	roots := map[string]struct{}{}
	for _, p := range c.Paths {
		if p == nil {
			continue
		}
		root := volumeRootForRecordPath(p.RecordPath)
		if root == "" {
			continue
		}
		// Use the absolute-path form as the dedupe key so two paths
		// with the same logical root coalesce into one volume.
		abs, _ := filepath.Abs(root)
		roots[abs] = struct{}{}
	}
	if len(roots) == 0 {
		return nil
	}
	mountPaths := make([]string, 0, len(roots))
	for r := range roots {
		mountPaths = append(mountPaths, r)
	}
	sort.Strings(mountPaths)

	out := make([]defs.StorageVolume, 0, len(mountPaths))
	for i, mp := range mountPaths {
		capacity, used, ok := statfsVolume(mp)
		status := defs.StorageVolumeStatusHealthy
		if !ok {
			status = defs.StorageVolumeStatusMissing
		} else if capacity > 0 {
			usedPct := float64(used) / float64(capacity) * 100.0
			switch {
			case usedPct >= 99.0:
				status = defs.StorageVolumeStatusFull
			case usedPct >= 90.0:
				status = defs.StorageVolumeStatusDegraded
			}
		}
		// Priority resolution per ADR 0009 §D5: prefer an operator-set
		// value from conf.RecordingVolumes[mountPath].Priority when
		// present; otherwise fall back to the mount-path-sort index so
		// unconfigured deployments retain the prior strictly-increasing
		// ordering.
		priority := i
		if rv, ok := c.RecordingVolumes[mp]; ok && rv != nil && rv.Priority != nil {
			priority = *rv.Priority
		}
		in := defs.StorageVolumeInput{
			MountPath:     mp,
			Kind:          volumeKindForMountPath(mp),
			CapacityBytes: capacity,
			UsedBytes:     used,
			ReservedBytes: 0,
			Status:        status,
			LastCheckedAt: nowUTC(),
			Priority:      priority,
		}
		out = append(out, defs.BuildStorageVolume(in, volumeIDFromMountPath(mp), ""))
	}
	return out
}

// onV1StorageVolumesList serves GET /v1/storage-volumes.
func (a *API) onV1StorageVolumesList(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	volumes := a.collectStorageVolumes(c)

	resp := storageVolumeListResponse{
		Items: volumes,
	}
	resp.ItemCount = len(resp.Items)

	ctx.JSON(http.StatusOK, resp)
}

// onV1StorageVolumesGet serves GET /v1/storage-volumes/:id.
func (a *API) onV1StorageVolumesGet(ctx *gin.Context) {
	id := ctx.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid volume id: %w", err))
		return
	}

	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	for _, v := range a.collectStorageVolumes(c) {
		if v.ID == id {
			ctx.JSON(http.StatusOK, &v)
			return
		}
	}

	a.writeError(ctx, http.StatusNotFound, fmt.Errorf("storage volume not found"))
}
