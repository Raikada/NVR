package snapshots

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/bluenviron/mediamtx/internal/vendorevents/amcrestchannel"
)

const maxSnapshotBytes = 8 << 20 // 8 MiB cap on camera responses

// fetch walks the ladder: Amcrest CGI → ONVIF snapshot URI → live
// frame grab. First success wins.
func (s *Service) fetch(ctx context.Context, info CameraInfo) ([]byte, error) {
	var errs []error

	if info.Channel == "amcrest" && info.Host != "" && info.Credentials != nil {
		cgi := url.URL{
			Scheme:   "http",
			Host:     info.Host,
			Path:     "/cgi-bin/snapshot.cgi",
			RawQuery: "channel=1",
		}
		if data, err := s.httpFetch(ctx, cgi.String(), info); err == nil {
			return data, nil
		} else {
			errs = append(errs, fmt.Errorf("amcrest cgi: %w", err))
		}
	}

	if info.SnapshotURI != "" && info.Credentials != nil {
		if data, err := s.httpFetch(ctx, info.SnapshotURI, info); err == nil {
			return data, nil
		} else {
			errs = append(errs, fmt.Errorf("onvif uri: %w", err))
		}
	}

	if s.grab != nil && info.Name != "" {
		if data, err := s.grab(ctx, info.Name); err == nil {
			return data, nil
		} else {
			errs = append(errs, fmt.Errorf("frame grab: %w", err))
		}
	}

	if len(errs) == 0 {
		return nil, fmt.Errorf("no snapshot source available")
	}
	return nil, fmt.Errorf("all snapshot sources failed: %v", errs)
}

// httpFetch GETs a camera URL with digest auth (Amcrest and ONVIF
// snapshot endpoints both speak RFC 2617).
func (s *Service) httpFetch(ctx context.Context, rawURL string, info CameraInfo) ([]byte, error) {
	username, password, err := info.Credentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	client := &http.Client{
		Transport: amcrestchannel.NewDigestTransport(username, password, nil),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSnapshotBytes))
	if err != nil {
		return nil, err
	}
	if _, _, err := jpegDims(data); err != nil {
		return nil, fmt.Errorf("response is not a JPEG: %w", err)
	}
	return data, nil
}
