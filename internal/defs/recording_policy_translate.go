package defs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// recordingConfigTuple captures the recording-related fields on a
// conf.Path that distinguish one RecordingPolicy from another. Two paths
// with identical tuples share a single synthesized policy.
type recordingConfigTuple struct {
	Record                bool
	RecordPath            string
	RecordFormat          conf.RecordFormat
	RecordPartDuration    time.Duration
	RecordMaxPartSize     int64
	RecordSegmentDuration time.Duration
	RecordDeleteAfter     time.Duration
}

// extractRecordingTuple pulls the recording-config fields off a conf.Path
// into a comparable tuple. Used by both the fingerprint helper and the
// synthesis dedup loop so they agree on what counts as "the same
// policy."
func extractRecordingTuple(p *conf.Path) recordingConfigTuple {
	return recordingConfigTuple{
		Record:                p.Record,
		RecordPath:            p.RecordPath,
		RecordFormat:          p.RecordFormat,
		RecordPartDuration:    time.Duration(p.RecordPartDuration),
		RecordMaxPartSize:     int64(p.RecordMaxPartSize),
		RecordSegmentDuration: time.Duration(p.RecordSegmentDuration),
		RecordDeleteAfter:     time.Duration(p.RecordDeleteAfter),
	}
}

// RecordingConfigFingerprint hashes a conf.Path's recording-config tuple
// into a deterministic short string used by the synthesis helper to
// dedup policies. SHA-256 truncated to 16 hex chars; collisions on this
// space are not a concern at the scale of "paths in one recorder's
// config."
func RecordingConfigFingerprint(p *conf.Path) string {
	t := extractRecordingTuple(p)
	// Build a stable string representation. Order matters; keep it stable
	// across releases or migration drifts when an old config is read.
	repr := fmt.Sprintf(
		"record=%t|path=%s|format=%s|part_dur=%d|max_part_size=%d|seg_dur=%d|delete_after=%d",
		t.Record,
		t.RecordPath,
		string(t.RecordFormat),
		t.RecordPartDuration,
		t.RecordMaxPartSize,
		t.RecordSegmentDuration,
		t.RecordDeleteAfter,
	)
	sum := sha256.Sum256([]byte(repr))
	return hex.EncodeToString(sum[:8])
}

// containerFromRecordFormat maps a conf.RecordFormat to canonical
// RecordingPolicyContainer. Unknown values default to fmp4 (the conf
// default).
func containerFromRecordFormat(rf conf.RecordFormat) RecordingPolicyContainer {
	switch rf {
	case conf.RecordFormatMPEGTS:
		return RecordingPolicyContainerMPEGTS
	default:
		return RecordingPolicyContainerFMP4
	}
}

// recordFormatFromContainer is the reverse mapping.
func recordFormatFromContainer(c RecordingPolicyContainer) conf.RecordFormat {
	switch c {
	case RecordingPolicyContainerMPEGTS:
		return conf.RecordFormatMPEGTS
	default:
		return conf.RecordFormatFMP4
	}
}

// RecordingPolicyFromPath extracts recording-related fields from a
// conf.Path into a canonical RecordingPolicy.
//
// Per ADR 0009 §"RecordingPolicy" amendments: the conf.Path
// RecordDeleteAfter consolidates into the canonical RetentionDuration
// (same concept). Mode defaults to "continuous" when Record=true and
// "off" otherwise; conf.Path has no scheduled-recording knob today.
//
// The resulting policy is named with the fingerprint suffix so that
// synthesized policies have a recognizable, deterministic display name.
func RecordingPolicyFromPath(p *conf.Path, policyID, tenantID string) RecordingPolicy {
	mode := RecordingPolicyModeOff
	if p.Record {
		mode = RecordingPolicyModeContinuous
	}

	pathTemplate := p.RecordPath
	var pathTemplatePtr *string
	if pathTemplate != "" {
		pathTemplatePtr = &pathTemplate
	}

	now := time.Now().UTC()
	return RecordingPolicy{
		ID:                 policyID,
		TenantID:           tenantID,
		Name:               "synthesized-" + RecordingConfigFingerprint(p),
		Mode:               mode,
		RetentionDuration:  time.Duration(p.RecordDeleteAfter),
		MinSegmentDuration: 0,
		MaxSegmentDuration: time.Duration(p.RecordSegmentDuration),
		Container:          containerFromRecordFormat(p.RecordFormat),
		Enabled:            p.Record,
		PartDuration:       time.Duration(p.RecordPartDuration),
		MaxPartSize:        int64(p.RecordMaxPartSize),
		RecordPathTemplate: pathTemplatePtr,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

// ApplyPolicyToPath stamps recording-related fields from a canonical
// RecordingPolicy onto a conf.Path. Used when a camera is created/updated
// and its referenced policy is resolved.
//
// Fields that have no canonical counterpart on the policy (e.g.,
// MinSegmentDuration, PreEventBuffer, PostEventBuffer, schedule windows)
// are not stamped; the recorder doesn't honor them today and Phase 2 is
// expected to wire them through if/when policy enforcement gains schedule
// support.
func ApplyPolicyToPath(p *conf.Path, policy RecordingPolicy) {
	p.Record = policy.Enabled && policy.Mode != RecordingPolicyModeOff
	if policy.RecordPathTemplate != nil {
		p.RecordPath = *policy.RecordPathTemplate
	}
	p.RecordFormat = recordFormatFromContainer(policy.Container)
	p.RecordPartDuration = conf.Duration(policy.PartDuration)
	p.RecordMaxPartSize = conf.StringSize(policy.MaxPartSize)
	p.RecordSegmentDuration = conf.Duration(policy.MaxSegmentDuration)
	p.RecordDeleteAfter = conf.Duration(policy.RetentionDuration)
}

// SynthesizePoliciesFromPaths walks a slice of conf.Paths and produces
// one canonical RecordingPolicy per unique combination of recording-config
// fields. Paths with identical recording configs share a policy.
//
// Returns:
//   - policies: deterministic slice of synthesized RecordingPolicy entries.
//     Order is by fingerprint for stability across runs.
//   - cameraNameToPolicyID: map from path-name to the synthesized policy id
//     that path's tuple landed on. The recorder maps path-name → camera-UUID
//     elsewhere; this layer keys by path-name since cameras get their UUIDs
//     during the same first-startup migration that consumes this map.
//
// Each synthesized policy gets a fresh UUID via the caller-supplied
// genUUID function so callers control the UUID source (test seeds,
// production crypto/rand, MS-issued UUIDs at pairing time, etc.).
func SynthesizePoliciesFromPaths(
	paths map[string]*conf.Path,
	tenantID string,
	genUUID func() string,
) (policies []RecordingPolicy, cameraNameToPolicyID map[string]string) {
	if genUUID == nil {
		// Fall back to a deterministic stub for callers that supply none;
		// the orchestrator/Phase 2 wires a real UUID source.
		var counter int
		genUUID = func() string {
			counter++
			return fmt.Sprintf("synth-policy-%08d", counter)
		}
	}

	cameraNameToPolicyID = make(map[string]string, len(paths))
	if len(paths) == 0 {
		return nil, cameraNameToPolicyID
	}

	// Group paths by fingerprint deterministically: sort path names first
	// so the first path encountered for a given tuple is stable across
	// runs, and assign UUIDs in that order.
	pathNames := make([]string, 0, len(paths))
	for name := range paths {
		pathNames = append(pathNames, name)
	}
	sort.Strings(pathNames)

	type bucket struct {
		policy RecordingPolicy
		paths  []string
	}
	buckets := make(map[string]*bucket)
	bucketOrder := make([]string, 0)

	for _, name := range pathNames {
		p := paths[name]
		if p == nil {
			continue
		}
		fp := RecordingConfigFingerprint(p)
		if existing, ok := buckets[fp]; ok {
			existing.paths = append(existing.paths, name)
			continue
		}
		policyID := genUUID()
		policy := RecordingPolicyFromPath(p, policyID, tenantID)
		buckets[fp] = &bucket{policy: policy, paths: []string{name}}
		bucketOrder = append(bucketOrder, fp)
	}

	policies = make([]RecordingPolicy, 0, len(bucketOrder))
	for _, fp := range bucketOrder {
		b := buckets[fp]
		policies = append(policies, b.policy)
		for _, name := range b.paths {
			cameraNameToPolicyID[name] = b.policy.ID
		}
	}
	return policies, cameraNameToPolicyID
}
