// Per-path RecordingPolicy resolver wired into the recordingmeta
// package. Translates "this path is recording right now → which policy
// governs it?" into the (policy_id, mode) tuple recordingmeta.Write
// persists in the sidecar.
//
// Lookup order mirrors api/api_v1_recordings.go's
// resolveContentTypeByCamera so on-disk sidecars and synthesizer
// fallbacks agree on a per-path resolution today: explicit
// path.RecordingPolicyID first, then conf.DefaultRecordingPolicyID,
// then unresolved (resolver returns ok=false and Write skips the
// sidecar — segment surfaces as continuous via the synthesizer's
// fallback, matching today's pre-amendment behavior).

package core

import (
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/recordingmeta"
)

func resolveRecordingMeta(c *conf.Conf, pathName string) (recordingmeta.Sidecar, bool) {
	if c == nil || c.Paths == nil {
		return recordingmeta.Sidecar{}, false
	}
	pathConf, _, err := conf.FindPathConf(c.Paths, pathName)
	if err != nil || pathConf == nil {
		return recordingmeta.Sidecar{}, false
	}
	policyID := pathConf.RecordingPolicyID
	if policyID == "" {
		policyID = conf.DefaultRecordingPolicyID
	}
	if c.RecordingPolicies == nil {
		return recordingmeta.Sidecar{}, false
	}
	policy, ok := c.RecordingPolicies[policyID]
	if !ok || policy == nil {
		return recordingmeta.Sidecar{}, false
	}
	return recordingmeta.Sidecar{
		PolicyID: policyID,
		Mode:     string(policy.Mode),
	}, true
}
