package api //nolint:revive

import (
	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// cameraIDFromPathName generates a deterministic UUIDv5 (SHA-1 namespace)
// for a conf.Path keyed by its Name. Per ADR 0009 §D4 the recorder
// generates Camera UUIDs locally during the pre-MS phase; using a
// deterministic source keyed off path-name means existing on-disk paths
// get a stable id across recorder restarts.
//
// The namespace is uuid.NameSpaceOID — chosen as a stable, well-known
// namespace that doesn't collide with any of the platform's other
// UUIDv5 spaces.
//
// Empty input returns "" rather than a derived UUID, so a stream
// session whose path is unknown surfaces no spurious camera_id (each
// such session would otherwise collide on the same UUIDv5 of "").
func cameraIDFromPathName(name string) string {
	if name == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

// cameraIDMaps walks the recorder's path table and produces forward
// (uuid → name) and reverse (name → uuid) lookups. The maps are derived
// fresh each call from the deterministic UUIDv5 of each path-name; an
// in-memory cache is unnecessary because the derivation is pure.
//
// Used by the /v1/cameras handlers to round-trip between the MediaMTX-
// internal path-name primary key and the canonical Camera UUID.
func cameraIDMaps(paths map[string]*conf.Path) (idToName map[string]string, nameToID map[string]string) {
	idToName = make(map[string]string, len(paths))
	nameToID = make(map[string]string, len(paths))
	for name := range paths {
		id := cameraIDFromPathName(name)
		idToName[id] = name
		nameToID[name] = id
	}
	return idToName, nameToID
}

// pathNameFromCameraID resolves a canonical Camera UUID to the
// recorder-internal conf.Path name. Returns the name and true on hit,
// "" and false on miss.
func pathNameFromCameraID(paths map[string]*conf.Path, cameraID string) (string, bool) {
	for name := range paths {
		if cameraIDFromPathName(name) == cameraID {
			return name, true
		}
	}
	return "", false
}

// validateCameraID parses a string param as a UUID. Returns the input
// string on success (callers compare against derived UUIDs as strings)
// and an error on parse failure.
func validateCameraID(s string) (string, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
