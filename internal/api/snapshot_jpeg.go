// Package api: snapshot JPEG conversion via in-process libav (cgo).
//
// The /v1/recorder/cameras/{id}/snapshot endpoint's first cut returned
// raw HLS-fragment bytes — technically functional but not fit for the
// typical "thumbnail in a UI" or "frame-as-evidence" use cases, since
// fMP4 fragments without their init segment are undecodable and even
// MPEG-TS fragments are short-video, not still-frame.
//
// An interim implementation shelled out to a system `ffmpeg` binary
// when one was on PATH. That worked but produced two operator-visible
// branches (ffmpeg-installed vs. not) and gave the API one extra wire
// shape clients had to switch on. The recorder now performs the
// conversion in-process via libav cgo bindings (`go-astiav`), which
// makes JPEG the only success path: any fragment that decodes returns
// JPEG, and the only fallback is the raw-fragment branch for
// undecodable inputs.
//
// This makes cgo a hard build requirement for the recorder. The
// project-level test Dockerfile installs `ffmpeg-dev`; cross-compile
// users need a cross-toolchain that supplies libav for their target.
package api //nolint:revive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/asticode/go-astiav"
)

// snapshotJPEGTimeout caps the time the recorder will spend decoding a
// fragment and encoding a JPEG. In-process libav handles a single-
// fragment keyframe extract in tens of milliseconds on commodity
// hardware; a few-second cap accommodates large frames or system load
// without letting a stuck decode tie up the request.
var snapshotJPEGTimeout = 5 * time.Second

// snapshotJPEGFromFragment decodes an HLS fragment in memory via libav
// (custom AVIO over a bytes.Reader), pulls the first decoded video
// frame, and re-encodes it as a JPEG. Returns the JPEG bytes on
// success; returns a wrapped error on any libav failure or on
// context-deadline expiry. The function makes no assumptions about
// the input format — libav's demuxer probe handles fMP4 (`ftyp...`)
// and MPEG-TS alike.
func snapshotJPEGFromFragment(ctx context.Context, fragment []byte) (_ []byte, retErr error) {
	if len(fragment) == 0 {
		return nil, fmt.Errorf("snapshot fragment is empty")
	}

	runCtx, cancel := context.WithTimeout(ctx, snapshotJPEGTimeout)
	defer cancel()

	// Honor the deadline at the boundaries we control. libav itself is
	// synchronous and doesn't accept a context, but this guards the
	// pre-decode setup and ensures a stuck call surfaces an error
	// rather than blocking the request handler indefinitely.
	if err := runCtx.Err(); err != nil {
		return nil, fmt.Errorf("snapshot context cancelled: %w", err)
	}

	reader := bytes.NewReader(fragment)

	// Allocate input format context. The custom IOContext below feeds
	// it bytes from `reader`; libav probes the format from the first
	// few KiB and dispatches to the right demuxer (mov for fMP4, mpegts
	// for MPEG-TS).
	inputFormatContext := astiav.AllocFormatContext()
	if inputFormatContext == nil {
		return nil, fmt.Errorf("allocating input format context failed")
	}
	defer inputFormatContext.Free()

	// 4 KiB AVIO buffer is plenty for a probe; libav grows it as
	// needed. write callback is nil (read-only); seek is required for
	// fMP4 (moov box can come after moof in some encoders, and the
	// demuxer seeks to it).
	ioContext, err := astiav.AllocIOContext(
		4096,
		false,
		func(b []byte) (int, error) {
			n, err := reader.Read(b)
			if errors.Is(err, io.EOF) {
				// libav expects ErrEof to terminate reads cleanly;
				// returning io.EOF maps to AVERROR_EOF inside the
				// binding.
				return n, astiav.ErrEof
			}
			return n, err
		},
		func(offset int64, whence int) (int64, error) {
			return reader.Seek(offset, whence)
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("allocating io context failed: %w", err)
	}
	defer ioContext.Free()

	inputFormatContext.SetPb(ioContext)

	if err := inputFormatContext.OpenInput("", nil, nil); err != nil {
		return nil, fmt.Errorf("opening input failed: %w", err)
	}
	defer inputFormatContext.CloseInput()

	if err := inputFormatContext.FindStreamInfo(nil); err != nil {
		return nil, fmt.Errorf("finding stream info failed: %w", err)
	}

	// Pick the first video stream. fMP4 fragments emitted by the
	// recorder's HLS muxer are video-only or video+audio; in either
	// case the video stream is what we want.
	var videoStream *astiav.Stream
	for _, s := range inputFormatContext.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			videoStream = s
			break
		}
	}
	if videoStream == nil {
		return nil, fmt.Errorf("no video stream in fragment")
	}

	// Allocate decoder.
	decCodec := astiav.FindDecoder(videoStream.CodecParameters().CodecID())
	if decCodec == nil {
		return nil, fmt.Errorf("decoder not found for codec id %d", videoStream.CodecParameters().CodecID())
	}
	decCodecContext := astiav.AllocCodecContext(decCodec)
	if decCodecContext == nil {
		return nil, fmt.Errorf("allocating decoder context failed")
	}
	defer decCodecContext.Free()

	if err := videoStream.CodecParameters().ToCodecContext(decCodecContext); err != nil {
		return nil, fmt.Errorf("populating decoder context failed: %w", err)
	}
	if err := decCodecContext.Open(decCodec, nil); err != nil {
		return nil, fmt.Errorf("opening decoder failed: %w", err)
	}

	pkt := astiav.AllocPacket()
	defer pkt.Free()
	frame := astiav.AllocFrame()
	defer frame.Free()

	// Decode loop: read packets, send to decoder, return on the first
	// successfully decoded frame. Most HLS fragments start with a
	// keyframe, so this typically resolves on the first iteration.
	var decoded bool
	for !decoded {
		if err := runCtx.Err(); err != nil {
			return nil, fmt.Errorf("snapshot deadline exceeded: %w", err)
		}
		readErr := inputFormatContext.ReadFrame(pkt)
		if readErr != nil && !errors.Is(readErr, astiav.ErrEof) {
			return nil, fmt.Errorf("reading packet failed: %w", readErr)
		}
		eof := errors.Is(readErr, astiav.ErrEof)

		if !eof && pkt.StreamIndex() != videoStream.Index() {
			pkt.Unref()
			continue
		}

		// On EOF we send a nil packet to flush the decoder.
		var sendErr error
		if eof {
			sendErr = decCodecContext.SendPacket(nil)
		} else {
			sendErr = decCodecContext.SendPacket(pkt)
			pkt.Unref()
		}
		if sendErr != nil && !errors.Is(sendErr, astiav.ErrEagain) {
			return nil, fmt.Errorf("sending packet to decoder failed: %w", sendErr)
		}

		for {
			recvErr := decCodecContext.ReceiveFrame(frame)
			if errors.Is(recvErr, astiav.ErrEagain) {
				break
			}
			if errors.Is(recvErr, astiav.ErrEof) {
				return nil, fmt.Errorf("decoder reached EOF without emitting a frame")
			}
			if recvErr != nil {
				return nil, fmt.Errorf("receiving frame from decoder failed: %w", recvErr)
			}
			decoded = true
			break
		}

		if eof && !decoded {
			return nil, fmt.Errorf("end of fragment reached without a decodable video frame")
		}
	}

	// Encode the decoded frame as JPEG. The mjpeg encoder accepts the
	// `yuvjXXXp` family (full-range JPEG variants of yuvXXXp). If the
	// decoded frame's pixel format is one of those it can be passed
	// straight through; for the more common yuvXXXp family we use
	// swscale to convert to the matching yuvjXXXp.
	jpegBytes, err := encodeFrameToJPEG(frame)
	if err != nil {
		return nil, fmt.Errorf("encoding frame to jpeg failed: %w", err)
	}
	if len(jpegBytes) == 0 {
		return nil, fmt.Errorf("mjpeg encoder produced empty output")
	}
	return jpegBytes, nil
}

// encodeFrameToJPEG converts a single decoded video frame into a JPEG
// byte slice using the mjpeg encoder. When the frame's pixel format
// is incompatible with mjpeg (the typical h264 decoder output is
// yuv420p; mjpeg wants yuvj420p), a software-scale step does the
// conversion.
func encodeFrameToJPEG(srcFrame *astiav.Frame) ([]byte, error) {
	encCodec := astiav.FindEncoder(astiav.CodecIDMjpeg)
	if encCodec == nil {
		return nil, fmt.Errorf("mjpeg encoder not found in this libav build")
	}

	width := srcFrame.Width()
	height := srcFrame.Height()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("decoded frame has invalid dimensions %dx%d", width, height)
	}

	// Pick a destination pixel format the mjpeg encoder supports.
	// Prefer yuvj420p (the full-range cousin of yuv420p that h264
	// streams commonly decode to); fall back to whatever the encoder
	// reports.
	dstPixFmt := astiav.PixelFormatYuvj420P
	supported := encCodec.SupportedPixelFormats()
	if len(supported) > 0 {
		match := false
		for _, p := range supported {
			if p == dstPixFmt {
				match = true
				break
			}
		}
		if !match {
			dstPixFmt = supported[0]
		}
	}

	encCodecContext := astiav.AllocCodecContext(encCodec)
	if encCodecContext == nil {
		return nil, fmt.Errorf("allocating encoder context failed")
	}
	defer encCodecContext.Free()

	encCodecContext.SetWidth(width)
	encCodecContext.SetHeight(height)
	encCodecContext.SetPixelFormat(dstPixFmt)
	encCodecContext.SetTimeBase(astiav.NewRational(1, 25))

	if err := encCodecContext.Open(encCodec, nil); err != nil {
		return nil, fmt.Errorf("opening mjpeg encoder failed: %w", err)
	}

	// Choose the frame to feed the encoder. If the source pixel
	// format already matches the encoder's input, send it directly;
	// otherwise convert via swscale.
	var encInput *astiav.Frame
	if srcFrame.PixelFormat() == dstPixFmt {
		encInput = srcFrame
	} else {
		swsCtx, err := astiav.CreateSoftwareScaleContext(
			width,
			height,
			srcFrame.PixelFormat(),
			width,
			height,
			dstPixFmt,
			astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
		)
		if err != nil {
			return nil, fmt.Errorf("creating sws context failed: %w", err)
		}
		defer swsCtx.Free()

		converted := astiav.AllocFrame()
		defer converted.Free()
		converted.SetWidth(width)
		converted.SetHeight(height)
		converted.SetPixelFormat(dstPixFmt)
		if err := converted.AllocBuffer(1); err != nil {
			return nil, fmt.Errorf("allocating destination frame buffer failed: %w", err)
		}
		if err := swsCtx.ScaleFrame(srcFrame, converted); err != nil {
			return nil, fmt.Errorf("scaling frame failed: %w", err)
		}
		// PTS is required by the encoder; the source frame's pts is
		// fine for a one-shot encode but swscale doesn't propagate it.
		converted.SetPts(srcFrame.Pts())
		encInput = converted
	}

	if err := encCodecContext.SendFrame(encInput); err != nil {
		return nil, fmt.Errorf("sending frame to encoder failed: %w", err)
	}
	// Flush — mjpeg is a single-frame codec, so a flush after the
	// frame produces the JPEG packet.
	if err := encCodecContext.SendFrame(nil); err != nil {
		return nil, fmt.Errorf("flushing encoder failed: %w", err)
	}

	encPkt := astiav.AllocPacket()
	defer encPkt.Free()

	if err := encCodecContext.ReceivePacket(encPkt); err != nil {
		return nil, fmt.Errorf("receiving encoded packet failed: %w", err)
	}
	// mjpeg packet data IS the JPEG byte stream — no muxing required.
	// Copy out before Unref/Free invalidates the underlying buffer.
	data := encPkt.Data()
	out := make([]byte, len(data))
	copy(out, data)
	encPkt.Unref()
	return out, nil
}
