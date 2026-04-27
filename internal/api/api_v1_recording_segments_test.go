package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestV1RecordingSegmentsList(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")
	writeSegmentFile(t, dir, "cam2", "2009-02-01_00-00-00-000000")

	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		ItemCount int `json:"item_count"`
		PageCount int `json:"page_count"`
		Items     []struct {
			ID          string `json:"id"`
			RecordingID string `json:"recording_id"`
			CameraID    string `json:"camera_id"`
			Path        string `json:"path"`
			State       string `json:"state"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 3, got.ItemCount)
	for _, it := range got.Items {
		require.Empty(t, it.Path, "segment path must not leak")
		require.NotEmpty(t, it.RecordingID, "recording_id must be backfilled per ADR 0009")
		_, perr := uuid.Parse(it.ID)
		require.NoError(t, perr, "segment id must be a UUID")
		_, perr = uuid.Parse(it.RecordingID)
		require.NoError(t, perr, "recording_id must be a UUID")
	}
}

func TestV1RecordingSegmentsListRecordingFilter(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-30-000000")
	writeSegmentFile(t, dir, "cam2", "2009-02-01_00-00-00-000000")

	// Discover the cam1 recording id.
	resp, err := http.Get(srv.URL + "/v1/recordings?camera_id=" + cameraIDFromPathName("cam1"))
	require.NoError(t, err)
	defer resp.Body.Close()
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)
	recID := listed.Items[0].ID

	resp2, err := http.Get(srv.URL + "/v1/recording-segments?recording_id=" + recID)
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got struct {
		ItemCount int `json:"item_count"`
		Items     []struct {
			RecordingID string `json:"recording_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got))
	require.Equal(t, 2, got.ItemCount)
	for _, it := range got.Items {
		require.Equal(t, recID, it.RecordingID)
	}
}

func TestV1RecordingSegmentsListCameraFilter(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")
	writeSegmentFile(t, dir, "cam2", "2008-11-07_11-22-00-000000")

	cam1 := cameraIDFromPathName("cam1")

	resp, err := http.Get(srv.URL + "/v1/recording-segments?camera_id=" + cam1)
	require.NoError(t, err)
	defer resp.Body.Close()
	var got struct {
		ItemCount int `json:"item_count"`
		Items     []struct {
			CameraID string `json:"camera_id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, cam1, got.Items[0].CameraID)
}

func TestV1RecordingSegmentsListStartedAndCreatedFilters(t *testing.T) {
	// started_after/before is footage time; created_after/before is
	// segment-write time. These dimensions must be filtered
	// independently per ADR 0009 §D5.
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	// Segment timestamped in 2008 ("footage") but written now
	// ("created"). started_before=2009 must include it; created_after
	// (now-1m) must also include it; created_before=epoch must exclude it.
	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")

	startedBefore := time.Date(2009, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	createdAfter := time.Now().Add(-time.Minute).Format(time.RFC3339)
	createdBefore := time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	// started_before should match.
	r1, err := http.Get(srv.URL + "/v1/recording-segments?started_before=" + startedBefore)
	require.NoError(t, err)
	defer r1.Body.Close()
	var g1 struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(r1.Body).Decode(&g1))
	require.Equal(t, 1, g1.ItemCount, "started_before filter is footage-time")

	// created_after now-1m should match (file was written above).
	r2, err := http.Get(srv.URL + "/v1/recording-segments?created_after=" + createdAfter)
	require.NoError(t, err)
	defer r2.Body.Close()
	var g2 struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(r2.Body).Decode(&g2))
	require.Equal(t, 1, g2.ItemCount, "created_after filter is segment-write time")

	// created_before=1970 should NOT match — file was written today.
	r3, err := http.Get(srv.URL + "/v1/recording-segments?created_before=" + createdBefore)
	require.NoError(t, err)
	defer r3.Body.Close()
	var g3 struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(r3.Body).Decode(&g3))
	require.Equal(t, 0, g3.ItemCount, "created_before filter must be distinct from started_before")
}

func TestV1RecordingSegmentsGet(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")

	// Discover the segment id via list.
	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)
	segID := listed.Items[0].ID

	resp2, err := http.Get(srv.URL + "/v1/recording-segments/" + segID)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var got struct {
		ID       string  `json:"id"`
		Path     string  `json:"path"`
		Checksum *string `json:"checksum,omitempty"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got))
	require.Equal(t, segID, got.ID)
	require.Empty(t, got.Path, "on-disk path must not leak")
}

func TestV1RecordingSegmentsGetInvalidUUID(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	resp, err := http.Get(srv.URL + "/v1/recording-segments/not-a-uuid")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestV1RecordingSegmentsGetNotFound(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	resp, err := http.Get(srv.URL + "/v1/recording-segments/" + uuid.New().String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestV1RecordingSegmentsDelete(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	segPath := writeSegmentFile(t, dir, "cam1", "2008-11-07_11-22-00-000000")

	// Discover the segment id.
	resp, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp.Body.Close()
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	require.Len(t, listed.Items, 1)
	segID := listed.Items[0].ID

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/recording-segments/"+segID, nil)
	require.NoError(t, err)
	delResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer delResp.Body.Close()
	require.Equal(t, http.StatusOK, delResp.StatusCode)

	// File must be gone on disk.
	_, statErr := os.Stat(segPath)
	require.True(t, os.IsNotExist(statErr), "segment file must be deleted from disk")

	// Re-listing must reflect the deletion (cache was invalidated).
	resp2, err := http.Get(srv.URL + "/v1/recording-segments")
	require.NoError(t, err)
	defer resp2.Body.Close()
	var got struct {
		ItemCount int `json:"item_count"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&got))
	require.Equal(t, 0, got.ItemCount)
}

func TestV1RecordingSegmentsDeleteInvalidUUID(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/recording-segments/not-a-uuid", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestV1RecordingSegmentsDeleteNotFound(t *testing.T) {
	dir, err := os.MkdirTemp("", "mediamtx-v1-segments")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	a := newTestAPI(t, dir)
	srv := newV1Server(t, a)

	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/v1/recording-segments/"+uuid.New().String(), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
