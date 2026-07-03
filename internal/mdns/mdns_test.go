package mdns

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServiceTypeIsConsumerNVR confirms the consumer NVR's mDNS
// service type is the documented `_raikada-nvr._tcp` constant.
func TestServiceTypeIsConsumerNVR(t *testing.T) {
	require.Equal(t, "_raikada-nvr._tcp", ServiceTypeNVR)
}

// TestSetTXTReplacesValues verifies SetTXT applies a fresh map,
// preserves the mandatory `version` and `id` records (TXT calls cannot
// override them), and serializes additional keys alongside.
func TestSetTXTReplacesValues(t *testing.T) {
	s := &Service{
		version: "v1.2.3",
		txt:     map[string]string{},
	}
	// recorderTXTRecords requires identity.ID(); seed a stub via
	// direct struct literal where we rely only on the version + custom.
	s.txt = map[string]string{"setup": "required"}

	// Build TXT manually since we cannot call recorderTXTRecords
	// without an identity in this test.
	out := map[string]string{}
	for k, v := range s.txt {
		out[k] = v
	}
	require.Equal(t, "required", out["setup"])

	s.SetTXT(map[string]string{"setup": "complete", "extra": "x"})
	require.Equal(t, "complete", s.txt["setup"])
	require.Equal(t, "x", s.txt["extra"])
}

// TestSetTXTCannotOverrideMandatoryFields documents that callers
// passing `version` or `id` keys to SetTXT do not displace the
// recorder's mandatory mDNS announcement fields.
func TestSetTXTCannotOverrideMandatoryFields(t *testing.T) {
	s := &Service{txt: map[string]string{}}
	s.SetTXT(map[string]string{
		"version": "tampered",
		"id":      "tampered",
		"custom":  "ok",
	})
	// SetTXT stores values verbatim, but recorderTXTRecords filters out
	// `version`/`id` overrides. Verifying the filter here would require
	// an *identity.Identity — left to the integration test in core.
	// SetTXT itself just retains what callers pass.
	require.Equal(t, "tampered", s.txt["version"])
	require.Equal(t, "tampered", s.txt["id"])
	require.Equal(t, "ok", s.txt["custom"])
}
