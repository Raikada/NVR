package store

import (
	"context"
	"testing"
	"time"
)

func TestClipSegments_InsertBatchAndList(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("seg-cam")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)

	segs := []*ClipSegment{
		{SegmentPath: "rec/2.mp4", StartOffsetMS: 1000, DurationMS: 500},
		{SegmentPath: "rec/1.mp4", StartOffsetMS: 0, DurationMS: 1000},
		{SegmentPath: "rec/3.mp4", StartOffsetMS: 1500, DurationMS: 250},
	}
	if err := s.ClipSegments.InsertBatch(ctx, c.ID, segs); err != nil {
		t.Fatalf("insert batch: %v", err)
	}

	got, err := s.ClipSegments.ListByClip(ctx, c.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("count: %d", len(got))
	}
	// Ordered by start_offset_ms ASC.
	if got[0].SegmentPath != "rec/1.mp4" || got[1].SegmentPath != "rec/2.mp4" || got[2].SegmentPath != "rec/3.mp4" {
		t.Errorf("ordering: %+v", got)
	}
}

func TestClipSegments_BatchEmpty(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("seg-empty")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)

	if err := s.ClipSegments.InsertBatch(ctx, c.ID, nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	got, _ := s.ClipSegments.ListByClip(ctx, c.ID)
	if len(got) != 0 {
		t.Errorf("count: %d", len(got))
	}
}

func TestClipSegments_DeleteByClip(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("seg-del")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)
	_ = s.ClipSegments.InsertBatch(ctx, c.ID, []*ClipSegment{
		{SegmentPath: "x", StartOffsetMS: 0, DurationMS: 100},
	})
	if err := s.ClipSegments.DeleteByClip(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.ClipSegments.ListByClip(ctx, c.ID)
	if len(got) != 0 {
		t.Errorf("not deleted: %+v", got)
	}
}

func TestClipSegments_CascadeOnClipDelete(t *testing.T) {
	s := mustOpenStore(t)
	ctx := context.Background()
	cam := newCamera("seg-casc")
	mustInsertCamera(t, s, cam)
	c := newClip(cam.ID, time.Now().UTC())
	_ = s.Clips.Insert(ctx, c)
	_ = s.ClipSegments.InsertBatch(ctx, c.ID, []*ClipSegment{
		{SegmentPath: "y", StartOffsetMS: 0, DurationMS: 100},
	})
	if err := s.Clips.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete clip: %v", err)
	}
	got, _ := s.ClipSegments.ListByClip(ctx, c.ID)
	if len(got) != 0 {
		t.Errorf("expected cascade; got %d", len(got))
	}
}
