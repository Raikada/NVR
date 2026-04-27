package defs

import (
	"time"
)

// EventSeverity is the severity classification of an Event.
type EventSeverity string

// Event severities per domain-model.md.
const (
	EventSeverityDebug    EventSeverity = "debug"
	EventSeverityInfo     EventSeverity = "info"
	EventSeverityWarning  EventSeverity = "warning"
	EventSeverityError    EventSeverity = "error"
	EventSeverityCritical EventSeverity = "critical"
)

// EventSubjectKind identifies the kind of canonical entity an Event
// refers to.
type EventSubjectKind string

// Event subject kinds per domain-model.md.
const (
	EventSubjectKindCamera  EventSubjectKind = "camera"
	EventSubjectKindStream  EventSubjectKind = "stream"
	EventSubjectKindSegment EventSubjectKind = "segment"
	EventSubjectKindVolume  EventSubjectKind = "volume"
	EventSubjectKindServer  EventSubjectKind = "server"
	EventSubjectKindUser    EventSubjectKind = "user"
	EventSubjectKindSession EventSubjectKind = "session"
)

// Event is the canonical Event entity emitted by the Recording Server
// and exposed at /v1/events per ADR 0009 §D2.
//
// Event `kind` is a closed, versioned vocabulary (e.g. camera.offline,
// segment.write_failed, storage.volume_full) defined centrally.
type Event struct {
	ID                string    `json:"id"`
	RecordingServerID string    `json:"recording_server_id"`
	TenantID          string    `json:"tenant_id"`
	SiteID            string    `json:"site_id"`
	OccurredAt        time.Time `json:"occurred_at"`

	Kind     string        `json:"kind"`
	Severity EventSeverity `json:"severity"`

	SubjectKind EventSubjectKind `json:"subject_kind"`
	SubjectID   string           `json:"subject_id"`

	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`

	CorrelationID *string `json:"correlation_id,omitempty"`
}
