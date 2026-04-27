package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// newV1EventsServer mounts the /v1/events handlers on a httptest
// server. The orchestrator owns the production route registration in
// api.go; tests bypass that to exercise the handlers in isolation.
func newV1EventsServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/events", a.onV1EventsList)
	r.GET("/v1/events/:id", a.onV1EventsGet)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// resetEventStoreSingleton clears the package-level default store so
// tests don't see each other's events.
func resetEventStoreSingleton(t *testing.T) {
	t.Helper()
	defaultEventStore().Clear()
	t.Cleanup(func() { defaultEventStore().Clear() })
}

func TestV1EventsListEmptyShowsNotice(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/events")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got eventListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 0, got.ItemCount)
	require.Empty(t, got.Items)
	require.NotEmpty(t, got.Notice, "empty store must surface the not-yet-wired notice")
	require.Contains(t, got.Notice, "events store not yet wired")
}

func TestV1EventsListReturnsPublishedEvents(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	cameraID := uuid.New().String()
	store.Publish(defs.EventInput{
		Kind:        "camera.online",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   cameraID,
		Message:     "camera came online",
	}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{
		Kind:        "segment.write_failed",
		Severity:    defs.EventSeverityError,
		SubjectKind: defs.EventSubjectKindSegment,
		SubjectID:   uuid.New().String(),
		Message:     "disk write failed",
	}, "rs", "tenant", "site")

	resp, err := http.Get(srv.URL + "/v1/events")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got eventListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 2, got.ItemCount)
	require.Empty(t, got.Notice, "non-empty store must not show the not-yet-wired notice")
}

func TestV1EventsListFiltersByCameraID(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	want := uuid.New().String()
	other := uuid.New().String()
	store.Publish(defs.EventInput{
		Kind: "camera.online", Severity: defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera, SubjectID: want,
	}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{
		Kind: "camera.offline", Severity: defs.EventSeverityWarning,
		SubjectKind: defs.EventSubjectKindCamera, SubjectID: other,
	}, "rs", "tenant", "site")

	q := url.Values{}
	q.Set("camera_id", want)
	resp, err := http.Get(srv.URL + "/v1/events?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got eventListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, want, got.Items[0].SubjectID)
}

func TestV1EventsListFiltersBySeverityMin(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	store.Publish(defs.EventInput{Kind: "x.debug", Severity: defs.EventSeverityDebug}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "x.info", Severity: defs.EventSeverityInfo}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "x.warning", Severity: defs.EventSeverityWarning}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "x.error", Severity: defs.EventSeverityError}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "x.critical", Severity: defs.EventSeverityCritical}, "rs", "tenant", "site")

	for _, c := range []struct {
		name      string
		query     string
		wantCount int
	}{
		{"explicit warning", "severity=warning", 3},
		{"prefix-form warning", "severity=>=warning", 3},
		{"severity_min error", "severity_min=error", 2},
		{"severity_min debug", "severity_min=debug", 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/v1/events?" + c.query)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var got eventListResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			require.Equal(t, c.wantCount, got.ItemCount, "query=%s", c.query)
		})
	}
}

func TestV1EventsListFiltersByKind(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	store.Publish(defs.EventInput{Kind: "camera.online", Severity: defs.EventSeverityInfo}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "camera.offline", Severity: defs.EventSeverityWarning}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "segment.write_failed", Severity: defs.EventSeverityError}, "rs", "tenant", "site")

	// Multi-value kind filter.
	resp, err := http.Get(srv.URL + "/v1/events?kind=camera.online&kind=segment.write_failed")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got eventListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 2, got.ItemCount)
}

func TestV1EventsListFiltersByTimeRange(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	t1 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	store.Publish(defs.EventInput{Kind: "k", Severity: defs.EventSeverityInfo, OccurredAt: t1}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "k", Severity: defs.EventSeverityInfo, OccurredAt: t2}, "rs", "tenant", "site")
	store.Publish(defs.EventInput{Kind: "k", Severity: defs.EventSeverityInfo, OccurredAt: t3}, "rs", "tenant", "site")

	q := url.Values{}
	q.Set("started_after", "2024-03-01T00:00:00Z")
	q.Set("started_before", "2024-09-01T00:00:00Z")
	resp, err := http.Get(srv.URL + "/v1/events?" + q.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got eventListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, t2, got.Items[0].OccurredAt)
}

func TestV1EventsListBadFilters(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	for _, c := range []struct {
		name  string
		query string
	}{
		{"camera_id non-uuid", "camera_id=not-a-uuid"},
		{"started_after bad", "started_after=not-a-date"},
		{"started_before bad", "started_before=not-a-date"},
		{"severity bad", "severity=bogus"},
		{"subject_kind bad", "subject_kind=bogus"},
		{"subject_id non-uuid", "subject_id=not-a-uuid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/v1/events?" + c.query)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode, "query=%s", c.query)
		})
	}
}

func TestV1EventsGet(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	store := defaultEventStore()
	e := store.Publish(defs.EventInput{
		Kind: "camera.online", Severity: defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera, SubjectID: uuid.New().String(),
	}, "rs", "tenant", "site")

	resp, err := http.Get(srv.URL + "/v1/events/" + e.ID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got defs.Event
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, e.ID, got.ID)
	require.Equal(t, "camera.online", got.Kind)
}

func TestV1EventsGetNotFound(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/events/" + uuid.New().String())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestV1EventsGetBadID(t *testing.T) {
	resetEventStoreSingleton(t)
	a := &API{}
	srv := newV1EventsServer(t, a)

	resp, err := http.Get(srv.URL + "/v1/events/not-a-uuid")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestParseEventSeverity(t *testing.T) {
	for _, c := range []struct {
		in       string
		want     defs.EventSeverity
		wantOK   bool
		wantSkip bool
	}{
		{"", "", true, true},
		{"warning", defs.EventSeverityWarning, true, false},
		{">=warning", defs.EventSeverityWarning, true, false},
		{">=critical", defs.EventSeverityCritical, true, false},
		{"bogus", "", false, false},
	} {
		got, ok := parseEventSeverity(c.in)
		require.Equal(t, c.wantOK, ok, "in=%q", c.in)
		require.Equal(t, c.want, got, "in=%q", c.in)
	}
}

func TestSeverityRank(t *testing.T) {
	require.Greater(t, severityRank(defs.EventSeverityWarning), severityRank(defs.EventSeverityInfo))
	require.Greater(t, severityRank(defs.EventSeverityError), severityRank(defs.EventSeverityWarning))
	require.Greater(t, severityRank(defs.EventSeverityCritical), severityRank(defs.EventSeverityError))
	require.Equal(t, 0, severityRank("unknown"))
}

// Smoke check that the eventListResponse omitempty for Notice survives
// a JSON round-trip when populated and when empty.
func TestEventListResponseNoticeOmitempty(t *testing.T) {
	withNotice := eventListResponse{Notice: "hi", Items: []defs.Event{}}
	b, err := json.Marshal(withNotice)
	require.NoError(t, err)
	require.Contains(t, string(b), `"notice":"hi"`)

	without := eventListResponse{Items: []defs.Event{}}
	b2, err := json.Marshal(without)
	require.NoError(t, err)
	require.NotContains(t, string(b2), `notice`, fmt.Sprintf("got %s", b2))
}
