// Package api: clip fmp4-to-mp4 remux via in-process libav (cgo).
//
// Replaces the byte-wise concat fallback with a real remux. The
// recorder writes its on-disk recording segments as self-contained
// fmp4 fragment files (one moov + moof+mdat fragments per file).
// Concatenating those byte-wise produces output that ffmpeg-class
// players can usually parse but standard QuickTime/Web players can
// not — each file's `moov` resets the timeline to zero, so the
// resulting bytes are not a single coherent mp4.
//
// This file performs a true demux/mux: it opens each segment with
// libav's `mov` demuxer, copies packets (no decode/encode) into a
// single `mp4` output muxer, rewrites stream indices to match the
// output, and offsets timestamps so dts/pts are monotonic across the
// segment boundary. The result is a single mp4 file with one moov,
// one set of streams, and a continuous timeline — playable by
// QuickTime, web `<video>` elements, ffprobe, VLC, etc.
//
// Per AGENTS.md §6 this file is in `internal/api/` and reads source
// segments through the public filesystem path; it does not touch
// internal/recorder, internal/recordstore, internal/stream, or any
// internal/servers tree.
package api //nolint:revive

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/asticode/go-astiav"
)

// remuxFmp4Segments remuxes the supplied fmp4 segment files (in
// order) into a single mp4 at outPath. Returns the produced file's
// byte size and sha256 checksum.
//
// Codec parameters are taken from the FIRST segment; subsequent
// segments are expected to share the same stream layout (the
// recorder writes uniform-codec segments per camera path). If a
// later segment has a stream the first did not, that stream's
// packets are dropped with no error — preserving the
// "best-effort export" posture rather than failing the clip.
//
// Timestamp continuity. After remuxing each segment, the helper
// tracks the highest end-DTS seen on any output stream (in that
// stream's output timebase) and converts it to a per-stream offset
// applied to all packets of the next segment. This produces a
// strictly monotonic dts on each stream across the whole
// concatenation. Audio and video streams are tracked independently
// because their timebases differ (90 kHz video vs. sample-rate
// audio is the common case).
// Trimming (SP4 follow-up). trims, when non-nil, carries one window
// per segment in segment-local time. Video trim-in snaps to the
// keyframe at or before SkipBefore (backward seek; stream copy can't
// start mid-GOP), audio follows the first kept video packet, and the
// output timeline is re-based so the clip starts at ~0.
func remuxFmp4Segments(segPaths []string, outPath string, trims []segmentTrim) (int64, string, error) {
	if len(segPaths) == 0 {
		return 0, "", errors.New("no segments to remux")
	}
	if trims != nil && len(trims) != len(segPaths) {
		return 0, "", errors.New("trim plan length mismatch")
	}

	// Allocate output format context up front. We pass an empty
	// filename: writes go through an explicitly-opened io context so
	// we can also hash the output bytes as they're produced.
	outputFC, err := astiav.AllocOutputFormatContext(nil, "mp4", "")
	if err != nil {
		return 0, "", fmt.Errorf("alloc output format context: %w", err)
	}
	if outputFC == nil {
		return 0, "", errors.New("output format context is nil")
	}
	defer outputFC.Free()

	// Open the first segment to read stream definitions; we'll mirror
	// them into the output and reuse the same shape for every later
	// segment.
	firstFC, err := openInputSegment(segPaths[0])
	if err != nil {
		return 0, "", fmt.Errorf("open first segment %s: %w", segPaths[0], err)
	}
	defer func() {
		firstFC.CloseInput()
		firstFC.Free()
	}()

	// Build the output streams from the first segment's audio+video
	// streams. We key by the source stream's index so later segments
	// can map their (possibly differently-ordered) streams in.
	type streamMap struct {
		out          *astiav.Stream
		outTimeBase  astiav.Rational
		mediaType    astiav.MediaType
		codecID      astiav.CodecID
		inIdx        int   // first-segment index, for reference
		dtsOffset    int64 // running offset in output timebase
		lastEndDts   int64 // dts+duration of last written packet, in output timebase
		trimBase     int64 // subtracted from segment-0 packets to re-base a trimmed timeline
		trimBaseSet  bool
	}

	// outputs is indexed by mediaType — audio/video — because that's
	// what survives across segments reliably. fmp4 segments emitted by
	// the same camera have stable per-mediatype shape.
	outputs := map[astiav.MediaType]*streamMap{}

	for _, is := range firstFC.Streams() {
		mt := is.CodecParameters().MediaType()
		if mt != astiav.MediaTypeVideo && mt != astiav.MediaTypeAudio {
			continue
		}
		if _, exists := outputs[mt]; exists {
			// Multiple streams of same media type — keep the first.
			continue
		}
		os := outputFC.NewStream(nil)
		if os == nil {
			return 0, "", errors.New("alloc output stream")
		}
		if err := is.CodecParameters().Copy(os.CodecParameters()); err != nil {
			return 0, "", fmt.Errorf("copy codec params: %w", err)
		}
		os.CodecParameters().SetCodecTag(0)
		// Mirror the input stream timebase so the output muxer has a
		// sensible default; mp4 muxer may override on WriteHeader.
		os.SetTimeBase(is.TimeBase())
		outputs[mt] = &streamMap{
			out:         os,
			outTimeBase: is.TimeBase(), // updated post-WriteHeader below
			mediaType:   mt,
			codecID:     is.CodecParameters().CodecID(),
			inIdx:       is.Index(),
		}
	}
	if len(outputs) == 0 {
		return 0, "", errors.New("first segment has no audio or video streams")
	}

	// Open output file via an explicit io context so we can tee the
	// muxed bytes through a sha256 hasher. The mp4 muxer needs seek
	// support (it patches the moov size after writing the trailer),
	// so we wrap an *os.File which supports both write+seek.
	outFile, err := os.Create(outPath)
	if err != nil {
		return 0, "", fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close() //nolint:errcheck

	// We allocate a custom AVIO context that writes into the file. We
	// hash on a final pass via os.Open after WriteTrailer; doing it
	// inline would conflict with the mp4 muxer's seek-back patches.
	ioCtx, err := astiav.AllocIOContext(
		4096,
		true, // writable
		nil,
		func(offset int64, whence int) (int64, error) {
			return outFile.Seek(offset, whence)
		},
		func(b []byte) (int, error) {
			return outFile.Write(b)
		},
	)
	if err != nil {
		return 0, "", fmt.Errorf("alloc io context: %w", err)
	}
	defer ioCtx.Free()
	outputFC.SetPb(ioCtx)

	// Write header. After this returns the output stream timebases
	// are finalized by the mp4 muxer; refresh our cached values.
	if err := outputFC.WriteHeader(nil); err != nil {
		return 0, "", fmt.Errorf("write header: %w", err)
	}
	for _, sm := range outputs {
		sm.outTimeBase = sm.out.TimeBase()
	}

	// We'll close the first input later in the segment loop; reset
	// the deferred close by clearing firstFC after the first iteration
	// uses it.
	consumedFirst := false

	pkt := astiav.AllocPacket()
	defer pkt.Free()

	// Loop through segments. The first segment uses the already-open
	// firstFC; subsequent segments are opened fresh.
	for segIdx, segPath := range segPaths {
		var inFC *astiav.FormatContext
		if !consumedFirst {
			inFC = firstFC
			consumedFirst = true
		} else {
			inFC, err = openInputSegment(segPath)
			if err != nil {
				return 0, "", fmt.Errorf("open segment %d (%s): %w", segIdx, segPath, err)
			}
		}

		// Trim window for this segment (nil trims = keep everything).
		var trim *segmentTrim
		if trims != nil {
			trim = &trims[segIdx]
		}
		if trim != nil && trim.SkipBefore > 0 {
			// Land on the keyframe at or before the trim-in point; a
			// failed seek degrades to forward keyframe-snapping in the
			// packet loop below.
			ts := trim.SkipBefore.Microseconds()
			if serr := inFC.SeekFrame(-1, ts, astiav.NewSeekFlags(astiav.SeekFlagBackward)); serr != nil {
				// Non-fatal: fall through and let the loop skip.
				_ = serr
			}
		}
		// videoStartSec is the segment-local time of the first kept
		// video packet: audio follows it so A/V stay in sync. -1 =
		// video not started (or no trim).
		videoStartSec := -1.0
		hasVideo := outputs[astiav.MediaTypeVideo] != nil
		if trim == nil || trim.SkipBefore <= 0 {
			videoStartSec = 0
		}

		// Build per-segment input-stream-index -> outputs entry.
		inIdxToOut := map[int]*streamMap{}
		for _, is := range inFC.Streams() {
			mt := is.CodecParameters().MediaType()
			sm, ok := outputs[mt]
			if !ok {
				continue
			}
			inIdxToOut[is.Index()] = sm
			// Stash the input timebase for this segment's rescale.
			// We attach it via a closure-local map below.
		}
		// Snapshot input timebases per stream-index for this segment.
		inTimeBases := map[int]astiav.Rational{}
		for _, is := range inFC.Streams() {
			inTimeBases[is.Index()] = is.TimeBase()
		}

		// Determine the offset applied to this segment's packets, per
		// output stream. For segment 0 the offset is 0; for later
		// segments the offset is the running dtsOffset already stored
		// on each streamMap (which was advanced at the end of the
		// previous segment's loop).
		// Track the per-output-stream maximum end-dts for THIS segment
		// so we can advance the offsets when we move on.
		segMaxEndDts := map[astiav.MediaType]int64{}

		for {
			readErr := inFC.ReadFrame(pkt)
			if readErr != nil {
				if errors.Is(readErr, astiav.ErrEof) {
					break
				}
				closeAndFree(inFC, segIdx > 0)
				return 0, "", fmt.Errorf("read packet from segment %d: %w", segIdx, readErr)
			}

			sm, ok := inIdxToOut[pkt.StreamIndex()]
			if !ok {
				pkt.Unref()
				continue
			}
			inTb, hasTb := inTimeBases[pkt.StreamIndex()]
			if !hasTb {
				pkt.Unref()
				continue
			}

			// Trim filtering, in segment-local seconds.
			if trim != nil {
				ptsSec := -1.0
				if pkt.Pts() != astiav.NoPtsValue {
					ptsSec = float64(pkt.Pts()) * inTb.Float64()
				}
				// Trim-out: drop packets starting past the window.
				if ptsSec >= 0 && ptsSec > trim.DropAfter.Seconds() {
					pkt.Unref()
					continue
				}
				// Trim-in.
				if trim.SkipBefore > 0 {
					switch sm.mediaType {
					case astiav.MediaTypeVideo:
						if videoStartSec < 0 {
							// Keep only from a keyframe on; post-seek the
							// first packet normally is one.
							if !pkt.Flags().Has(astiav.PacketFlagKey) {
								pkt.Unref()
								continue
							}
							videoStartSec = ptsSec
						}
					case astiav.MediaTypeAudio:
						start := trim.SkipBefore.Seconds()
						if hasVideo {
							if videoStartSec < 0 {
								pkt.Unref()
								continue
							}
							start = videoStartSec
						}
						if ptsSec >= 0 && ptsSec < start {
							pkt.Unref()
							continue
						}
					}
				}
			}

			// Rescale into output timebase, then apply the running
			// offset so the timeline stays monotonic.
			pkt.SetStreamIndex(sm.out.Index())
			pkt.RescaleTs(inTb, sm.outTimeBase)

			// Re-base a trimmed first segment so the output starts at
			// ~0 rather than at the trim-in offset.
			if segIdx == 0 && trim != nil && trim.SkipBefore > 0 {
				if !sm.trimBaseSet && pkt.Dts() != astiav.NoPtsValue {
					sm.trimBase = pkt.Dts()
					sm.trimBaseSet = true
				}
				if sm.trimBaseSet {
					if pkt.Pts() != astiav.NoPtsValue {
						pkt.SetPts(pkt.Pts() - sm.trimBase)
					}
					if pkt.Dts() != astiav.NoPtsValue {
						pkt.SetDts(pkt.Dts() - sm.trimBase)
					}
				}
			}

			if pkt.Pts() != astiav.NoPtsValue {
				pkt.SetPts(pkt.Pts() + sm.dtsOffset)
			}
			if pkt.Dts() != astiav.NoPtsValue {
				pkt.SetDts(pkt.Dts() + sm.dtsOffset)
			}
			pkt.SetPos(-1)

			endDts := int64(0)
			if pkt.Dts() != astiav.NoPtsValue {
				endDts = pkt.Dts() + pkt.Duration()
			}
			if cur, has := segMaxEndDts[sm.mediaType]; !has || endDts > cur {
				segMaxEndDts[sm.mediaType] = endDts
			}
			sm.lastEndDts = endDts

			if err := outputFC.WriteInterleavedFrame(pkt); err != nil {
				pkt.Unref()
				closeAndFree(inFC, segIdx > 0)
				return 0, "", fmt.Errorf("write packet from segment %d: %w", segIdx, err)
			}
			pkt.Unref()
		}

		// Advance per-stream offsets by the highest end-dts observed
		// on each output stream during this segment. If a stream had
		// no packets in this segment its offset stays put — when the
		// stream resumes in a later segment, a new offset will be
		// applied based on whatever the running max becomes; this is
		// good enough for the typical "every segment carries the same
		// streams" case the recorder produces.
		for mt, end := range segMaxEndDts {
			if sm := outputs[mt]; sm != nil {
				sm.dtsOffset = end
			}
		}

		if segIdx > 0 {
			inFC.CloseInput()
			inFC.Free()
		}
	}

	if err := outputFC.WriteTrailer(); err != nil {
		return 0, "", fmt.Errorf("write trailer: %w", err)
	}

	// Sync and stat the file, then hash it. We do a separate read
	// pass (as opposed to a tee'd hasher during write) because the
	// mp4 muxer seeks back to patch the moov size; tee'd hashing
	// would mis-hash the rewritten bytes.
	if err := outFile.Sync(); err != nil {
		return 0, "", fmt.Errorf("sync output: %w", err)
	}
	if _, err := outFile.Seek(0, io.SeekStart); err != nil {
		return 0, "", fmt.Errorf("seek output: %w", err)
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, outFile)
	if err != nil {
		return 0, "", fmt.Errorf("hash output: %w", err)
	}

	return size, "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// openInputSegment opens the given file as an input format context
// using libav's format probe. Returns the opened context (caller
// owns CloseInput + Free).
func openInputSegment(path string) (*astiav.FormatContext, error) {
	fc := astiav.AllocFormatContext()
	if fc == nil {
		return nil, errors.New("alloc input format context")
	}
	if err := fc.OpenInput(path, nil, nil); err != nil {
		fc.Free()
		return nil, fmt.Errorf("open input: %w", err)
	}
	if err := fc.FindStreamInfo(nil); err != nil {
		fc.CloseInput()
		fc.Free()
		return nil, fmt.Errorf("find stream info: %w", err)
	}
	return fc, nil
}

// closeAndFree closes+frees a non-firstFC input context. The first
// segment's context is owned by the deferred close in the caller.
func closeAndFree(fc *astiav.FormatContext, ownsClose bool) {
	if ownsClose {
		fc.CloseInput()
		fc.Free()
	}
}

