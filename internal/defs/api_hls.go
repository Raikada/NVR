package defs

import (
	"time"

	"github.com/google/uuid"
)

// APIHLSServer contains methods used by the API and Metrics server.
type APIHLSServer interface {
	APISessionsList() (*APIHLSSessionList, error)
	APISessionsGet(uuid.UUID) (*APIHLSSession, error)
	APISessionsKick(uuid.UUID) error
	APIMuxersList() (*APIHLSMuxerList, error)
	APIMuxersGet(string) (*APIHLSMuxer, error)
}

// APIHLSSessionList is a list of HLS sessions.
type APIHLSSessionList struct {
	ItemCount int             `json:"itemCount"`
	PageCount int             `json:"pageCount"`
	Items     []APIHLSSession `json:"items"`
}

// APIHLSSession is an HLS session.
type APIHLSSession struct {
	TenantID      string    `json:"tenantId"`
	ID            uuid.UUID `json:"id"`
	Created       time.Time `json:"created"`
	RemoteAddr    string    `json:"remoteAddr"`
	Path          string    `json:"path"`
	Query         string    `json:"query"`
	User          string    `json:"user"`
	OutboundBytes uint64    `json:"outboundBytes"`
}

// APIHLSMuxerList is a list of HLS muxers.
//
// JSON tags are snake_case to match the canonical /v1 pagination
// convention (item_count, page_count); see ADR 0009 OpenAPI spec
// intro. The /v1/recorder/hls-muxers endpoint accepts the matching
// items_per_page query parameter.
type APIHLSMuxerList struct {
	ItemCount int           `json:"item_count"`
	PageCount int           `json:"page_count"`
	Items     []APIHLSMuxer `json:"items"`
}

// APIHLSMuxer is an HLS muxer.
type APIHLSMuxer struct {
	TenantID                string    `json:"tenantId"`
	Path                    string    `json:"path"`
	Created                 time.Time `json:"created"`
	LastRequest             time.Time `json:"lastRequest"`
	OutboundBytes           uint64    `json:"outboundBytes"`
	OutboundFramesDiscarded uint64    `json:"outboundFramesDiscarded"`
	// deprecated
	BytesSent uint64 `json:"bytesSent" deprecated:"true"`
}
