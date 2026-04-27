// Package api — escape-hatch wire shapes per ADR 0009 §D6.
//
// These shapes back the /v1/recorder/... namespace. They are deliberately
// recorder-local: they are not platform-canonical types (per §D6.1, the MS
// does not project the escape hatch upward), so they live in
// internal/api/ rather than internal/defs/. Each shape exposes a subset
// of recorder-internal config that doesn't fit cleanly into a canonical
// entity:
//
//   - RecorderConfig — operational config (logging, timeouts, server
//     bind addresses, TLS material refs, recorder-level shell hooks).
//     Mirrors ~95% of conf.GlobalConf with credential redaction applied
//     on read.
//   - CameraDefaults — non-policy default fields applied at camera
//     creation (RTSP transport, on-demand defaults, etc.). Anything that
//     maps to RecordingPolicy is intentionally excluded; per §D6, the
//     canonical mechanism for shared camera behavior is to point cameras
//     at a common RecordingPolicy.
//   - HLSMuxer — per-camera HLS muxer state. Recorder-internal
//     observability; the canonical model has no place for muxer-state.
//   - RPiCameraSourceConfig — RPi-camera ~30-field source-config sub
//     shape. Hardware-specific; explicitly carved out of canonical
//     Camera.source_config per §D5 Cameras / §D6 contents.
//   - CameraHooks — per-camera shell hooks. Recorder-script behavior
//     that the platform can't sensibly reason about across tiers.
//
// Shape composition mirrors the underlying conf types so that PATCH
// payloads round-trip cleanly through copyStructFields. The wire JSON
// tags match the established camelCase MediaMTX convention so existing
// tooling stays compatible.
package api //nolint:revive
