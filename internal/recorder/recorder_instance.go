package recorder

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
	"github.com/bluenviron/mediamtx/internal/stream"
)

// segmentWriteFailedPublisher is the package-level hook the
// recorder uses to emit segment.write_failed events. core.go wires
// it to api.PublishSegmentWriteFailed at startup; tests replace it
// with a recording stub. Kept as a package-level function (not
// threaded through recorderInstance) to mirror the existing
// pipeline-target pattern api.SetPipelineEventTarget uses for
// camera.online / camera.offline — both deliberately avoid
// breaking the layering between media pipeline and api package.
var segmentWriteFailedPublisher func(pathName, segmentPath, reason string)

// SetSegmentWriteFailedPublisher wires the publisher used by
// recorder_instance.run() when a SegmentWriteError reaches the
// reader-error channel. Pass nil to clear (used by tests).
func SetSegmentWriteFailedPublisher(fn func(pathName, segmentPath, reason string)) {
	segmentWriteFailedPublisher = fn
}

type recorderInstance struct {
	pathFormat        string
	format            conf.RecordFormat
	partDuration      time.Duration
	maxPartSize       conf.StringSize
	segmentDuration   time.Duration
	pathName          string
	stream            *stream.Stream
	onSegmentCreate   OnSegmentCreateFunc
	onSegmentComplete OnSegmentCompleteFunc
	parent            logger.Writer

	streamID    uuid.UUID
	pathFormat2 string
	format2     format
	skip        bool
	reader      *stream.Reader

	terminate chan struct{}
	done      chan struct{}
}

// Log implements logger.Writer.
func (ri *recorderInstance) Log(level logger.Level, format string, args ...any) {
	ri.parent.Log(level, format, args...)
}

func (ri *recorderInstance) initialize() {
	ri.streamID = uuid.New()
	ri.pathFormat2 = ri.pathFormat
	ri.pathFormat2 = recordstore.PathAddExtension(
		strings.ReplaceAll(ri.pathFormat2, "%path", ri.pathName),
		ri.format,
	)
	ri.reader = &stream.Reader{
		SkipOutboundBytes: true,
		Parent:            ri,
	}

	ri.terminate = make(chan struct{})
	ri.done = make(chan struct{})

	switch ri.format {
	case conf.RecordFormatMPEGTS:
		ri.format2 = &formatMPEGTS{
			ri: ri,
		}
		ok := ri.format2.initialize()
		ri.skip = !ok

	default:
		ri.format2 = &formatFMP4{
			ri: ri,
		}
		ok := ri.format2.initialize()
		ri.skip = !ok
	}

	if !ri.skip {
		ri.stream.AddReader(ri.reader)
	}

	go ri.run()
}

func (ri *recorderInstance) close() {
	close(ri.terminate)
	<-ri.done
}

func (ri *recorderInstance) run() {
	defer close(ri.done)

	if !ri.skip {
		select {
		case err := <-ri.reader.Error():
			ri.Log(logger.Error, err.Error())

			// Differentiate disk-write failures from the
			// heterogeneous error population (network read,
			// codec parse, etc.) that flows through this
			// channel. Only segment-write errors emit
			// segment.write_failed; other errors stay in
			// the log line above.
			var swe *SegmentWriteError
			if errors.As(err, &swe) && segmentWriteFailedPublisher != nil {
				reason := ""
				if swe.Inner != nil {
					reason = swe.Inner.Error()
				}
				segmentWriteFailedPublisher(ri.pathName, swe.Path, reason)
			}

		case <-ri.terminate:
		}

		ri.stream.RemoveReader(ri.reader)
	} else {
		<-ri.terminate
	}

	ri.format2.close()
}
