// Package pairing implements the recorder's MS-pairing client per
// platform/docs/api-contracts/recorder-management-pairing.md.
//
// The flow runs in the recorder's process: the operator triggers it
// from the recorder's setup wizard with {ms_url, token, optional
// root_fingerprint}; the recorder builds a CSR using its
// internal/identity keypair, POSTs to the MS, long-polls the status
// endpoint, and on approval persists the issued cert + chain +
// pinned roots back into identity. The browser drives state via two
// recorder-local endpoints: POST /v1/recorder/pair to start, and
// GET /v1/recorder/pair/status to observe.
package pairing

import (
	"time"
)

// State is the recorder's pairing-flow state. Start at Idle; success
// path is Idle → InProgress → Approved. Failures land in
// Rejected / Failed / TokenExpired / TokenConsumedElsewhere /
// AlreadyPaired. Terminal states surface to the operator UI; Idle
// after a terminal state is reached by the operator dismissing the
// result.
type State string

const (
	StateIdle                   State = "idle"
	StateInProgress             State = "in_progress"
	StateApproved               State = "approved"
	StateRejected               State = "rejected"
	StateTokenExpired           State = "token_expired"
	StateTokenConsumedElsewhere State = "token_consumed_elsewhere"
	StateFailed                 State = "failed"
	StateAlreadyPaired          State = "already_paired"
)

// Status is the JSON shape returned by GET /v1/recorder/pair/status.
type Status struct {
	State            State     `json:"state"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
	MSURL            string    `json:"ms_url,omitempty"`
	PairingRequestID string    `json:"pairing_request_id,omitempty"`
	Detail           string    `json:"detail,omitempty"`
}

// StartRequest is the body of POST /v1/recorder/pair.
type StartRequest struct {
	MSURL           string `json:"ms_url"`
	Token           string `json:"token"`
	RootFingerprint string `json:"root_fingerprint,omitempty"` // sha256 hex; from QR
}

// StartResult is the synchronous response to POST /v1/recorder/pair.
type StartResult struct {
	State            State  `json:"state"`
	PairingRequestID string `json:"pairing_request_id,omitempty"`
	Detail           string `json:"detail,omitempty"`
}

// Wire shapes mirroring the MS contract §5.

type submitRequest struct {
	Token                     string         `json:"token"`
	ProposedRecordingServerID string         `json:"proposed_recording_server_id"`
	CSRPEM                    string         `json:"csr_pem"`
	DeviceMetadata            map[string]any `json:"device_metadata"`
}

type submitResponse struct {
	PairingRequestID string    `json:"pairing_request_id"`
	State            string    `json:"state"`
	SubmittedAt      time.Time `json:"submitted_at"`
	StatusURL        string    `json:"status_url"`
	ApprovalRequired bool      `json:"approval_required"`
}

type statusResponse struct {
	PairingRequestID string `json:"pairing_request_id"`
	State            string `json:"state"`

	// Present when state == "approved".
	ApprovedAt        time.Time          `json:"approved_at,omitempty"`
	RecordingServer   *recordingServer   `json:"recording_server,omitempty"`
	IssuedCertificate *issuedCertificate `json:"issued_certificate,omitempty"`
	MSMetadata        *msMetadata        `json:"ms_metadata,omitempty"`

	// Present when state == "rejected".
	RejectedAt time.Time `json:"rejected_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`

	// Present when state == "pending_approval".
	SubmittedAt    time.Time `json:"submitted_at,omitempty"`
	TokenExpiresAt time.Time `json:"token_expires_at,omitempty"`
}

type recordingServer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SiteID    string    `json:"site_id,omitempty"`
	TenantID  string    `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type issuedCertificate struct {
	CertPEM   string    `json:"cert_pem"`
	ChainPEM  string    `json:"chain_pem"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

type msMetadata struct {
	IssuerURL    string `json:"issuer_url"`
	JWKSURL      string `json:"jwks_url"`
	RootsURL     string `json:"roots_url"`
	WebSocketURL string `json:"websocket_url"`
}

// rootsResponse is the shape of GET /.well-known/raikada-roots.
type rootsResponse struct {
	Roots             []rootEntry `json:"roots"`
	ActiveFingerprint string      `json:"active_fingerprint"`
}

type rootEntry struct {
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	CertPEM           string    `json:"cert_pem"`
	Kind              string    `json:"kind"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	ChainPEM          string    `json:"chain_pem,omitempty"`
}

// errorResponse mirrors the contract §1 error envelope.
type errorResponse struct {
	Err struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}
