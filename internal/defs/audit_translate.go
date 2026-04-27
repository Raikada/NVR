// Package defs: canonical-JSON serializer and SHA-256 chain helpers
// for AuditLogEntry per ADR 0006 D3.
//
// Canonicalization rules per ADR 0006 D3:
//   - Deterministic key ordering by lexicographic sort.
//   - No insignificant whitespace.
//   - RFC 3339 timestamp formatting (delegated to time.Time's MarshalJSON,
//     which produces RFC 3339 by default — verified by the
//     TestCanonicalJSON_Determinism test).
//   - Refs serialized as their canonical id form (string fields, no
//     wrapping object).
//   - No number-formatting variation (the AuditLogEntry shape has no
//     numeric fields, so this rule is satisfied trivially).
//
// Implementation note: Go's encoding/json marshals map keys in sorted
// order and struct fields in declaration order. We deliberately route
// the canonicalization through a generic any (decoded from the
// struct's standard JSON form) so we can re-emit with explicitly
// sorted keys at every nested level. This guards against future
// additions where someone might add a map[string]X field whose
// iteration is non-deterministic in some Go version, or a nested
// struct whose declaration-order serialization isn't lexicographic.
package defs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalJSONAuditLogEntry serializes an AuditLogEntry to canonical
// JSON per ADR 0006 D3. The serialization is deterministic for any
// given logical input.
func CanonicalJSONAuditLogEntry(entry AuditLogEntry) ([]byte, error) {
	// Round-trip via encoding/json to a generic any tree, then re-emit
	// with sorted keys at every level. Round-tripping picks up the
	// existing JSON tags / omitempty rules, so the canonical form
	// matches the wire form exactly except for key ordering and
	// whitespace.
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("marshal audit entry: %w", err)
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode audit entry tree: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeCanonical writes a canonical-JSON encoding of v to buf:
// objects with sorted keys, arrays in order, no insignificant
// whitespace.
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		// json.Marshal handles string escaping per the JSON spec.
		b, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(b)
	case json.Number:
		// Numbers are emitted in the form they were parsed; we
		// captured them with UseNumber() above. AuditLogEntry has no
		// numeric fields today so this branch is for future safety.
		buf.WriteString(x.String())
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return fmt.Errorf("canonical-json: unsupported type %T", v)
	}
	return nil
}

// ComputeAuditEntryHash computes the SHA-256 of the canonical-JSON
// serialization of entry with its EntryHash field cleared to the
// empty string. The PrevHash field is included in the hashed bytes —
// that is the chain link.
//
// Hex-encoded; matches the wire form of EntryHash and PrevHash.
func ComputeAuditEntryHash(entry AuditLogEntry) (string, error) {
	entry.EntryHash = ""
	canon, err := CanonicalJSONAuditLogEntry(entry)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}
