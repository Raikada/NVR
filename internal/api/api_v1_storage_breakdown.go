// Package api: GET /v1/storage-volumes/breakdown — per-content-type
// storage rollup per the 2026-05-06 RecordingSegment.content_type
// amendment.
//
// Wave 5 ships the v1 minimum: returns total bytes and segment count
// grouped three ways (overall by content_type, per-camera, per-volume).
// The recorder doesn't persist a segment index today (synthesis re-walks
// recordstore on every call), so the rollup is computed on demand from
// the same synthesize() pass /v1/recordings and /v1/recording-segments
// already use. That means the cost is bounded by the segment count, not
// by an extra disk traversal — synthesize() already populated the
// in-memory map and is cached until a config change.
//
// Permission gate: storage.read (same as /v1/storage-volumes/{id} —
// breakdown is observability over the same volume surface).
package api //nolint:revive

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

// breakdownEntry is the fixed (content_type, byte_size, segment_count)
// triple returned in every grouping. snake_case per ADR 0009 conventions.
type breakdownEntry struct {
	ContentType  string `json:"content_type"`
	ByteSize     int64  `json:"byte_size"`
	SegmentCount int    `json:"segment_count"`
}

// breakdownByCamera surfaces the per-camera roll-up; nested
// by_content_type lists the per-content-type triples for each camera.
type breakdownByCamera struct {
	CameraID      string           `json:"camera_id"`
	ByContentType []breakdownEntry `json:"by_content_type"`
	ByteSize      int64            `json:"byte_size"`
	SegmentCount  int              `json:"segment_count"`
}

// breakdownByVolume surfaces the per-volume roll-up. The recorder
// pre-Wave-5 didn't stamp volume_id onto synthesized segments (D8
// follow-up; see api_v1_recordings.go::synthesize), so segments today
// land under volume_id="" — we still emit the entry under that empty
// string so the wire shape is self-describing. When volume-stamping
// lands, this surface fills in without a wire-shape change.
type breakdownByVolume struct {
	VolumeID      string           `json:"volume_id"`
	MountPath     string           `json:"mount_path"`
	ByContentType []breakdownEntry `json:"by_content_type"`
	ByteSize      int64            `json:"byte_size"`
	SegmentCount  int              `json:"segment_count"`
}

// storageBreakdownResponse is the on-wire shape of GET
// /v1/storage-volumes/breakdown. The four blocks are independent slices
// of the same segment population: clients pick the grouping they need.
type storageBreakdownResponse struct {
	ByContentType []breakdownEntry    `json:"by_content_type"`
	ByCamera      []breakdownByCamera `json:"by_camera"`
	ByVolume      []breakdownByVolume `json:"by_volume"`
	TotalBytes    int64               `json:"total_bytes"`
	SegmentCount  int                 `json:"segment_count"`
}

// onV1StorageVolumesBreakdown serves
// GET /v1/storage-volumes/breakdown.
//
// Per Wave 5 plus the platform amendment (2026-05-06), this rollups
// the synthesized segment population by:
//
//   - by_content_type — overall sum across every segment.
//   - by_camera — same, partitioned by camera_id, then by content_type.
//   - by_volume — same, partitioned by volume_id, then by content_type.
//
// Each entry carries `byte_size` and `segment_count`. Top-level
// `total_bytes` and `segment_count` are sums across all segments —
// equivalent to summing every by_content_type entry, but kept on the
// envelope so callers don't have to recompute.
//
// Sorting: the slices are sorted lexicographically (content_type,
// camera_id, volume_id) so consecutive calls return the same wire
// order. Nested by_content_type entries are also sorted by
// content_type for the same reason.
func (a *API) onV1StorageVolumesBreakdown(ctx *gin.Context) {
	_, segs := a.synthesize()

	// Top-level: content_type → (byte_size, segment_count)
	byContentType := make(map[string]*breakdownEntry)
	// Per-camera: camera_id → (content_type → triple)
	byCameraContentType := make(map[string]map[string]*breakdownEntry)
	// Per-volume: volume_id → (content_type → triple); we also remember
	// the mount_path under which a volume_id was last seen so the wire
	// surface stays useful pre-volume-stamping (mount_path will be ""
	// today; see comment on breakdownByVolume).
	byVolumeContentType := make(map[string]map[string]*breakdownEntry)
	volumeMountPath := make(map[string]string)

	var totalBytes int64
	totalCount := 0

	// Walk the synthesized segment population once and roll up.
	for _, s := range segs {
		ct := string(s.ContentType)
		if ct == "" {
			// Defense in depth: SegmentFromRecordstoreFile already
			// surfaces "" as continuous, but if a future code path
			// constructs a RecordingSegment without going through that
			// translator, classify here so the rollup math stays sane.
			ct = "continuous"
		}

		// Top-level
		entry, ok := byContentType[ct]
		if !ok {
			entry = &breakdownEntry{ContentType: ct}
			byContentType[ct] = entry
		}
		entry.ByteSize += s.SizeBytes
		entry.SegmentCount++

		// Per-camera
		camMap, ok := byCameraContentType[s.CameraID]
		if !ok {
			camMap = make(map[string]*breakdownEntry)
			byCameraContentType[s.CameraID] = camMap
		}
		camEntry, ok := camMap[ct]
		if !ok {
			camEntry = &breakdownEntry{ContentType: ct}
			camMap[ct] = camEntry
		}
		camEntry.ByteSize += s.SizeBytes
		camEntry.SegmentCount++

		// Per-volume
		volMap, ok := byVolumeContentType[s.VolumeID]
		if !ok {
			volMap = make(map[string]*breakdownEntry)
			byVolumeContentType[s.VolumeID] = volMap
		}
		volEntry, ok := volMap[ct]
		if !ok {
			volEntry = &breakdownEntry{ContentType: ct}
			volMap[ct] = volEntry
		}
		volEntry.ByteSize += s.SizeBytes
		volEntry.SegmentCount++
		// Mount-path is best-effort: synthesize doesn't currently set
		// volume_id (D8 follow-up); we keep the lookup for forward
		// compatibility.
		if _, set := volumeMountPath[s.VolumeID]; !set {
			volumeMountPath[s.VolumeID] = ""
		}

		totalBytes += s.SizeBytes
		totalCount++
	}

	resp := storageBreakdownResponse{
		ByContentType: flattenContentTypeMap(byContentType),
		ByCamera:      flattenByCamera(byCameraContentType),
		ByVolume:      flattenByVolume(byVolumeContentType, volumeMountPath),
		TotalBytes:    totalBytes,
		SegmentCount:  totalCount,
	}

	ctx.JSON(http.StatusOK, &resp)
}

// flattenContentTypeMap collapses a content_type → entry map into a
// stable-sorted slice.
func flattenContentTypeMap(m map[string]*breakdownEntry) []breakdownEntry {
	out := make([]breakdownEntry, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, *m[k])
	}
	return out
}

// flattenByCamera collapses per-camera maps into a stable-sorted slice
// keyed on camera_id ascending. Per-camera totals are computed fresh
// from the inner triples to avoid drift if the inner numbers were
// adjusted.
func flattenByCamera(m map[string]map[string]*breakdownEntry) []breakdownByCamera {
	out := make([]breakdownByCamera, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, cam := range keys {
		entries := flattenContentTypeMap(m[cam])
		row := breakdownByCamera{
			CameraID:      cam,
			ByContentType: entries,
		}
		for _, e := range entries {
			row.ByteSize += e.ByteSize
			row.SegmentCount += e.SegmentCount
		}
		out = append(out, row)
	}
	return out
}

// flattenByVolume mirrors flattenByCamera but for the per-volume
// rollup. mount_path is looked up from the supplied auxiliary map; an
// empty mount_path indicates the recorder hasn't stamped volume_id on
// segments yet (D8 follow-up).
func flattenByVolume(
	m map[string]map[string]*breakdownEntry,
	mountPaths map[string]string,
) []breakdownByVolume {
	out := make([]breakdownByVolume, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, vol := range keys {
		entries := flattenContentTypeMap(m[vol])
		row := breakdownByVolume{
			VolumeID:      vol,
			MountPath:     mountPaths[vol],
			ByContentType: entries,
		}
		for _, e := range entries {
			row.ByteSize += e.ByteSize
			row.SegmentCount += e.SegmentCount
		}
		out = append(out, row)
	}
	return out
}
