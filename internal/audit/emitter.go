// Package audit holds the per-domain audit emitter helpers Phase 6
// wires across the recorder. Phase 6 Task 6.4 imports this so the
// emitter is constructible at startup; Task 6.7 fleshes out the
// per-domain helpers (Auth.LoginSuccess, Camera.CredentialsRotated,
// etc.) with explicit allow-list redaction at write time.
package audit

import (
	"github.com/bluenviron/mediamtx/internal/store"
)

// Emitter wraps the recorder's AuditLogRepo and exposes per-domain
// helper sub-emitters (see auth.go, camera.go, etc.) that each handler
// uses to record a single audit row with explicit allow-list redaction.
type Emitter struct {
	repo *store.AuditLogRepo
}

// New constructs an Emitter pinned to the supplied repo.
func New(repo *store.AuditLogRepo) *Emitter {
	return &Emitter{repo: repo}
}

// Repo returns the underlying repository. Tests use this to assert
// rows were appended; production code should prefer the per-domain
// helpers.
func (e *Emitter) Repo() *store.AuditLogRepo {
	if e == nil {
		return nil
	}
	return e.repo
}
