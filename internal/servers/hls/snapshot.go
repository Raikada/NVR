package hls

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// ErrMuxerNoContent indicates that the muxer for a given path is not
// currently producing media (no instance, no segments yet, or content
// did not become available within the snapshot deadline). API callers
// should map this to HTTP 503.
var ErrMuxerNoContent = errors.New("muxer is not producing media")

// snapshotDeadline caps the time the snapshot path will spend waiting
// for the muxer's HTTP machinery to respond at any single stage. The
// gohlslib muxer can block waiting for content to become available;
// callers expect a quick yes-or-no, so we stop after a short window.
const snapshotDeadline = 2 * time.Second

// muxerSnapshot drives the gohlslib muxer's HTTP surface in-process to
// extract the most recent media segment for a given camera. The flow
// mirrors what an HLS client does:
//
//  1. GET index.m3u8 → multivariant playlist.
//  2. Parse the leading EXT-X-STREAM-INF URI to find the variant
//     playlist filename (e.g. main_stream.m3u8 / video1_stream.m3u8).
//  3. GET that variant playlist.
//  4. Walk EXTINF / segment-URI pairs and pick the last one (the most
//     recent finalized segment).
//  5. GET that segment's bytes.
//
// We deliberately bypass the recorder's own HLS HTTP server (and its
// session-cookie / path-auth machinery) and hit the gohlslib muxer's
// dispatcher directly. That dispatcher routes by basename only, so no
// session is required. This is read-only observability: we never
// publish, mutate, or otherwise touch the media pipeline. Per
// AGENTS.md §6, observability around the pipeline is allowed when
// explicitly requested; per ADR 0009 §D6 the snapshot lives in the
// recorder-localized escape hatch because no canonical Snapshot
// entity exists.
//
// The returned content type is what gohlslib emits for the segment:
// video/mp4 for the fMP4 variants and video/MP2T for MPEG-TS. The /v1
// snapshot endpoint passes that through with a spec note that the
// payload is a raw HLS segment, not a JPEG. Producing a JPEG would
// require demuxing the segment, decoding a video frame, and
// re-encoding — all of which would require a new third-party encoder
// dependency — so we explicitly fall back to raw segment bytes per
// the snapshot scope decision.
func (m *muxer) apiSnapshot() (data []byte, contentType string, err error) {
	m.mutex.RLock()
	instance := m.instance
	m.mutex.RUnlock()

	if instance == nil {
		return nil, "", ErrMuxerNoContent
	}

	deadline := time.Now().Add(snapshotDeadline)

	// Step 1: multivariant playlist.
	mvCode, _, mvBody, err := instance.snapshotFetch("index.m3u8", deadline)
	if err != nil {
		return nil, "", err
	}
	if mvCode != http.StatusOK {
		return nil, "", fmt.Errorf("multivariant playlist returned %d", mvCode)
	}

	variantURI, ok := firstVariantURI(mvBody)
	if !ok {
		return nil, "", ErrMuxerNoContent
	}

	// Step 2: variant playlist.
	varCode, _, varBody, err := instance.snapshotFetch(variantURI, deadline)
	if err != nil {
		return nil, "", err
	}
	if varCode != http.StatusOK {
		return nil, "", fmt.Errorf("variant playlist returned %d", varCode)
	}

	segURI, ok := lastSegmentURI(varBody)
	if !ok {
		return nil, "", ErrMuxerNoContent
	}

	// Step 3: segment bytes.
	segCode, segCT, segBody, err := instance.snapshotFetch(segURI, deadline)
	if err != nil {
		return nil, "", err
	}
	if segCode != http.StatusOK {
		return nil, "", fmt.Errorf("segment fetch returned %d", segCode)
	}

	return segBody, segCT, nil
}

// snapshotFetch issues a single in-process request against the
// muxer's gohlslib dispatcher. The dispatcher routes by basename
// only, so we strip any query string out of the URI parameter for
// the request path while preserving it in the URL the handler sees
// (gohlslib's media-playlist handler reads from r.URL.Query()).
//
// The deadline guards against gohlslib's media-playlist handler,
// which uses sync.Cond.Wait to block until segments become available;
// for an idle muxer that wait would never return. We run the fetch
// on a goroutine and bail if the deadline elapses, returning
// ErrMuxerNoContent as a normal "not producing media" signal.
func (mi *muxerInstance) snapshotFetch(uri string, deadline time.Time) (int, string, []byte, error) {
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return 0, "", nil, ErrMuxerNoContent
	}

	// Strip query when computing the basename used for routing, but
	// keep it on the request URL so handlers that read query params
	// see what they expect.
	path := uri
	rawQuery := ""
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		path = uri[:i]
		rawQuery = uri[i+1:]
	}

	req := httptest.NewRequest(http.MethodGet, "http://snapshot/"+path, nil)
	if rawQuery != "" {
		req.URL.RawQuery = rawQuery
	}
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mi.hmuxer.Handle(w, req)
	}()

	select {
	case <-done:
		ct := w.Header().Get("Content-Type")
		return w.Code, ct, w.Body.Bytes(), nil
	case <-time.After(timeout):
		return 0, "", nil, ErrMuxerNoContent
	}
}

// firstVariantURI scans a multivariant HLS playlist for the URI that
// follows the first EXT-X-STREAM-INF tag. That URI is the variant
// playlist filename we hit next.
func firstVariantURI(playlist []byte) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(playlist))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	expectURI := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if expectURI {
			if !strings.HasPrefix(line, "#") {
				return line, true
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			expectURI = true
		}
	}
	return "", false
}

// lastSegmentURI walks an HLS media playlist and returns the segment
// URI that follows the final EXTINF tag — the most recent finalized
// segment in the muxer's window.
func lastSegmentURI(playlist []byte) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(playlist))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	last := ""
	expectURI := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if expectURI {
			if !strings.HasPrefix(line, "#") {
				last = line
				expectURI = false
			}
			continue
		}
		if strings.HasPrefix(line, "#EXTINF") {
			expectURI = true
		}
	}
	if last == "" {
		return "", false
	}
	return last, true
}
