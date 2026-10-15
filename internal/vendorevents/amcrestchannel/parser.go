// Package amcrestchannel implements the Amcrest/Dahua CGI event
// channel: a long-lived multipart stream from
// eventManager.cgi?action=attach parsed into normalized events.
//
// Block parsing semantics ported from the amcrest-sdk project
// (Apache-2.0), reimplemented for this repo's needs.
package amcrestchannel

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bluenviron/mediamtx/internal/vendorevents"
)

// cgiEvent is one parsed multipart block:
// Code=X;action=Y;index=Z[;data={...json...}]  or a bare "Heartbeat".
type cgiEvent struct {
	Code     string
	Action   string
	Index    int
	DataJSON string
}

// parseBlock parses the lines of one multipart block. Returns nil for
// unrecognized content.
func parseBlock(lines []string) *cgiEvent {
	var headerLine string
	var jsonLines []string
	inJSON := false
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "Content-") {
			continue
		}
		if headerLine == "" && strings.HasPrefix(trimmed, "Code=") {
			headerLine = trimmed
			if strings.HasSuffix(trimmed, "{") || strings.Contains(trimmed, "data={") {
				inJSON = true
				braceDepth = 1
			}
			continue
		}
		if headerLine == "" && trimmed == "Heartbeat" {
			return &cgiEvent{Code: "Heartbeat"}
		}
		if inJSON {
			jsonLines = append(jsonLines, line)
			braceDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if braceDepth <= 0 {
				inJSON = false
			}
		}
	}
	if headerLine == "" {
		return nil
	}

	ev := &cgiEvent{}
	header := headerLine
	if i := strings.Index(header, ";data="); i >= 0 {
		header = header[:i]
	}
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		key, val := part[:eq], strings.TrimSpace(part[eq+1:])
		switch key {
		case "Code":
			ev.Code = val
		case "action":
			ev.Action = val
		case "index":
			fmt.Sscanf(val, "%d", &ev.Index) //nolint:errcheck
		}
	}
	if ev.Code == "" {
		return nil
	}
	if len(jsonLines) > 0 {
		// The header carried "data={"; re-attach the opening brace.
		ev.DataJSON = "{" + strings.Join(jsonLines, "\n")
	}
	return ev
}

// codeMap: Amcrest event code → seeded event_types id. Only listed
// codes emit; everything else (storage, config, heartbeat) is dropped.
var codeMap = map[string]string{
	"VideoMotion":          "motion",
	"SmartMotionHuman":     "person",
	"SmartMotionVehicle":   "vehicle",
	"CrossLineDetection":   "line_cross",
	"CrossRegionDetection": "motion",
	"VideoBlind":           "tamper",
	"AudioMutation":        "audio_alarm",
	"AlarmLocal":           "io_in",
}

// doorbellCodes emit regardless of action — AD410 firmwares vary
// between CallNoAnswered / Invite / _DoTalkAction_ for a button press.
var doorbellCodes = map[string]bool{
	"CallNoAnswered": true,
	"Invite":         true,
	"_DoTalkAction_": true,
}

// mapEvent normalizes a parsed CGI event. emit=false means drop.
func mapEvent(ev *cgiEvent) (vendorevents.NormalizedEvent, bool) {
	if doorbellCodes[ev.Code] {
		return normalized("doorbell", ev), true
	}
	typeID, ok := codeMap[ev.Code]
	if !ok || ev.Action != "Start" {
		return vendorevents.NormalizedEvent{}, false
	}
	return normalized(typeID, ev), true
}

func normalized(typeID string, ev *cgiEvent) vendorevents.NormalizedEvent {
	payload, _ := json.Marshal(map[string]any{
		"code":   ev.Code,
		"action": ev.Action,
		"index":  ev.Index,
		"data":   json.RawMessage(orNull(ev.DataJSON)),
	})
	return vendorevents.NormalizedEvent{
		TypeID:   typeID,
		Severity: "info",
		Payload:  payload,
	}
}

func orNull(s string) string {
	if strings.TrimSpace(s) == "" {
		return "null"
	}
	// Vendor JSON blobs are occasionally malformed; keep the payload
	// valid by quoting anything that doesn't parse.
	if !json.Valid([]byte(s)) {
		quoted, _ := json.Marshal(s)
		return string(quoted)
	}
	return s
}
