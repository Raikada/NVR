// Package api: Wave 6 software-update applier wiring per ADR 0014.
//
// The applier package (internal/softwareupdate) is concerned with the
// install + restart + health-check + rollback machinery. The API
// surface owns the inbound HTTP endpoint (api_v1_recorder_software_
// update.go) and the audit-chain seam (this file).
//
// Audit emit follows the camerasync.AuditEmitter / policysync.Audit
// Emitter pattern: a thin adapter turns string action+outcome+attrs
// triples into properly-chained defs.AuditLogEntryInput rows.
package api //nolint:revive

import (
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/softwareupdate"
)

// SoftwareUpdateAuditEmitter returns a softwareupdate.AuditEmitter
// backed by this API's audit chain. Used by Core when constructing
// the applier.
func (a *API) SoftwareUpdateAuditEmitter() softwareupdate.AuditEmitter {
	return &softwareUpdateAuditEmitter{a: a}
}

type softwareUpdateAuditEmitter struct {
	a *API
}

// Emit writes a software_update.* audit entry. outcome is one of
// "success" | "failure" | "denied" (string-mapped to defs.AuditOutcome).
func (e *softwareUpdateAuditEmitter) Emit(action, outcome string, attrs map[string]string) {
	var oc defs.AuditOutcome
	switch outcome {
	case "success":
		oc = defs.AuditOutcomeSuccess
	case "denied":
		oc = defs.AuditOutcomeDenied
	default:
		oc = defs.AuditOutcomeFailure
	}
	resourceID := ""
	if attrs != nil {
		resourceID = attrs["lifecycle"]
	}
	e.a.emitAudit(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		ActorID:      "ms-service-software-update-push",
		Action:       action,
		Outcome:      oc,
		ResourceKind: "software_update_lifecycle",
		ResourceID:   resourceID,
		Attributes:   attrs,
	})
}
