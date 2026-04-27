// Package recorder: segment-write error type used to differentiate
// disk-write failures from the heterogeneous error population that
// flows through the reader-error channel in recorder_instance.go.
//
// Without differentiation, emitting one canonical Event kind
// (`segment.write_failed`) over every error reaching that channel
// would mislead — a network read failure or a codec parse failure
// would surface as a "write failed" event. SegmentWriteError marks
// errors that genuinely originated from a disk-write site (file
// create, directory create, write, sync, close) inside the format
// writers, so recorder_instance.run() can use errors.As to detect
// the class and emit only when appropriate.
//
// Per AGENTS.md §6, this thin error-wrap is the minimal change
// required to support the canonical Event producer; it does not
// alter any media-pipeline semantics. Callers in format_fmp4.go /
// format_mpegts.go wrap their disk-IO errors at the syscall
// boundary and return them unchanged through the existing error
// chain.
package recorder

// SegmentWriteError wraps an error that originated at a disk-write
// site for a recording segment. Path is the segment file path being
// written when the failure occurred (may be empty if the failure
// happened before path resolution, e.g., directory creation).
// Format identifies the recorder format that produced the error
// (`fmp4` or `mpegts`) for attribute attachment in the emitted
// Event.
//
// The type is tested via errors.As; the SegmentWriteError zero
// value is meaningless. Callers always construct via &SegmentWriteError{...}.
type SegmentWriteError struct {
	Path   string
	Format string
	Inner  error
}

// Error implements the error interface. The Inner error's message
// is returned verbatim so existing log lines continue to read the
// same way; the wrap is structural, not cosmetic.
func (e *SegmentWriteError) Error() string {
	if e == nil || e.Inner == nil {
		return "segment write error"
	}
	return e.Inner.Error()
}

// Unwrap supports errors.Is / errors.As traversal so callers can
// reach the underlying os/io error if they want to inspect it.
func (e *SegmentWriteError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Inner
}

// wrapFMP4WriteErr wraps err with SegmentWriteError tagged as fmp4
// when err is non-nil. Returns err unchanged when nil. The helper
// keeps wrap sites at the disk-IO boundary one-line and consistent.
func wrapFMP4WriteErr(path string, err error) error {
	if err == nil {
		return nil
	}
	return &SegmentWriteError{Path: path, Format: "fmp4", Inner: err}
}

// wrapMPEGTSWriteErr wraps err with SegmentWriteError tagged as
// mpegts when err is non-nil.
func wrapMPEGTSWriteErr(path string, err error) error {
	if err == nil {
		return nil
	}
	return &SegmentWriteError{Path: path, Format: "mpegts", Inner: err}
}
