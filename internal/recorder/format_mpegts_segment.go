package recorder

import (
	"os"
	"path/filepath"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

type formatMPEGTSSegment struct {
	pathFormat2       string
	flush             func() error
	onSegmentCreate   OnSegmentCreateFunc
	onSegmentComplete OnSegmentCompleteFunc
	startDTS          time.Duration
	startNTP          time.Time
	log               logger.Writer

	path      string
	fi        *os.File
	lastFlush time.Duration
	lastDTS   time.Duration
}

func (s *formatMPEGTSSegment) initialize() {
	s.lastFlush = s.startDTS
	s.lastDTS = s.startDTS
}

func (s *formatMPEGTSSegment) close() error {
	err := s.flush()
	if err != nil {
		// flush forwards bufio buffer to s.fi; failure here is a
		// disk-write failure regardless of where Close lands.
		err = wrapMPEGTSWriteErr(s.path, err)
	}

	if s.fi != nil {
		s.log.Log(logger.Debug, "closing segment %s", s.path)
		err2 := s.fi.Close()
		if err == nil && err2 != nil {
			err = wrapMPEGTSWriteErr(s.path, err2)
		}

		// Clear the in-flight registry entry; see the matching comment
		// in formatFMP4Segment.close().
		recordstore.UnregisterCurrentSegment(s.path)

		if err2 == nil {
			duration := s.lastDTS - s.startDTS
			s.onSegmentComplete(s.path, duration)
		}
	}

	return err
}

func (s *formatMPEGTSSegment) Write(p []byte) (int, error) {
	if s.fi == nil {
		s.path = recordstore.Path{Start: s.startNTP}.Encode(s.pathFormat2)
		s.log.Log(logger.Debug, "creating segment %s", s.path)

		err := os.MkdirAll(filepath.Dir(s.path), 0o755)
		if err != nil {
			return 0, wrapMPEGTSWriteErr(s.path, err)
		}

		fi, err := os.Create(s.path)
		if err != nil {
			return 0, wrapMPEGTSWriteErr(s.path, err)
		}

		// Register the segment as in-flight; see the matching comment
		// in formatFMP4Segment.closeCurPart().
		recordstore.RegisterCurrentSegment(s.path)

		s.onSegmentCreate(s.path)

		s.fi = fi
	}

	n, err := s.fi.Write(p)
	if err != nil {
		return n, wrapMPEGTSWriteErr(s.path, err)
	}
	return n, nil
}
