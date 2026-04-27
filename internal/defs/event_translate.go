package defs

import (
	"time"
)

// EventInput is the recorder's internal "I want to emit an event"
// payload. The recorder doesn't have a clean canonical-shape internal
// Event type yet (per ADR 0009 §D2 the Event entity is defined but the
// recorder's emission pipeline is currently informal logger calls); this
// shape is the named input contract for the canonical translator.
//
// Phase 2D wires the recorder's existing event-shaped log paths through
// EventInput and BuildEvent.
type EventInput struct {
	Kind        string
	Severity    EventSeverity
	SubjectKind EventSubjectKind
	SubjectID   string

	Message    string
	Attributes map[string]string

	// OccurredAt may be zero; BuildEvent defaults to time.Now() in that
	// case.
	OccurredAt time.Time

	// CorrelationID is optional.
	CorrelationID *string
}

// BuildEvent synthesizes a canonical Event from internal EventInput plus
// the recorder's per-process tenancy context. ADR 0009 §D2 calls Event
// "an existing canonical entity, owned by Recording Server"; the recorder
// emits these. The event id is supplied by the caller (UUID source per
// ADR 0009 D4).
//
// Used as the seam for Phase 2D when the recorder's internal event-shaped
// log paths get folded into a real Event-emission pipeline.
func BuildEvent(
	in EventInput,
	eventID string,
	recordingServerID string,
	tenantID string,
	siteID string,
) Event {
	occurred := in.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	return Event{
		ID:                eventID,
		RecordingServerID: recordingServerID,
		TenantID:          tenantID,
		SiteID:            siteID,
		OccurredAt:        occurred,
		Kind:              in.Kind,
		Severity:          in.Severity,
		SubjectKind:       in.SubjectKind,
		SubjectID:         in.SubjectID,
		Message:           in.Message,
		Attributes:        in.Attributes,
		CorrelationID:     in.CorrelationID,
	}
}
