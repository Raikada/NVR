// jti-keyed session lifecycle tracker for the auth.session_started
// Event producer.
//
// Per ADR 0011 §"Consequences": JWT validation establishes the moment
// "session begins for this token"; subsequent same-`jti` requests
// deduplicate to the same logical session. The recorder needs a tiny
// in-memory registry that says "have I seen this jti before?" so the
// auth middleware can emit `auth.session_started` once per session
// rather than once per request.
//
// Design notes:
//   - Keyed by jti (raw JWT id claim). jti is opaque from the
//     recorder's perspective; we don't validate its shape, just dedup
//     on byte equality.
//   - TTL slightly longer than ADR 0011's max access-token lifetime
//     (15 min). Entries prune ~30 min after first-seen so a token
//     used twice across its full lifetime still hits the dedup, but
//     the registry doesn't grow unbounded.
//   - Pruning is opportunistic on Touch — no background goroutine,
//     no scheduler hook. Worst case a quiet recorder holds a few
//     hundred stale entries until the next request arrives, which is
//     fine.
//   - The registry is process-wide (mirrors EventStore /
//     defaultAuditChain) so any API instance shares the dedup state.
//     Running multiple API instances in one process would be unusual
//     but the singleton tolerates it.
package api

import (
	"sync"
	"time"
)

// jtiSessionTTL is the window during which a jti is treated as the
// same logical session. Comfortably above ADR 0011 D1's max access-
// token lifetime (15 minutes) so a same-jti request near token expiry
// still dedups; tokens past expiry fail validation upstream and never
// reach this code path anyway.
const jtiSessionTTL = 30 * time.Minute

// jtiSessionRegistry tracks first-seen timestamps for jti claims.
// Sessions are pruned lazily on Touch.
type jtiSessionRegistry struct {
	mu        sync.Mutex
	firstSeen map[string]time.Time
}

// Touch records a sighting of jti and reports whether this is the
// first time the registry has seen it within the TTL window. Returns
// true on first-seen (caller emits auth.session_started); false on
// subsequent calls. An empty jti always returns false — the pre-ADR-
// 0011 internal/HTTP auth paths produce empty RawJTI and have no
// session-start semantics.
func (r *jtiSessionRegistry) Touch(jti string, now time.Time) (firstSeen bool) {
	if jti == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.firstSeen == nil {
		r.firstSeen = make(map[string]time.Time)
	}

	// Opportunistic prune: walk and drop entries past TTL. The map is
	// small (one entry per active session) and sessions are short-
	// lived, so the cost is negligible compared to the per-request
	// overhead the API already accepts (auth, audit, etc.).
	cutoff := now.Add(-jtiSessionTTL)
	for k, t := range r.firstSeen {
		if t.Before(cutoff) {
			delete(r.firstSeen, k)
		}
	}

	if _, seen := r.firstSeen[jti]; seen {
		// Refresh first-seen so an active session that uses the token
		// continuously beyond the original TTL stays deduped. This
		// matches "same logical session" semantics — the session is
		// alive as long as requests keep coming, not based on when the
		// first request happened to arrive.
		r.firstSeen[jti] = now
		return false
	}
	r.firstSeen[jti] = now
	return true
}

// jtiSessions is the process-wide registry.
var jtiSessions = &jtiSessionRegistry{}
