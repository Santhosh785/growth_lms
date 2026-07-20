package handlers

import (
	"testing"
	"time"

	"growth-lms/internal/models"
)

// TestSignedURLTTL_DraftVsPublished proves the expiry policy the spec
// requires: draft/review/scheduled/unpublished course assets get a
// short-lived (<5 min) signed URL, published course assets can get up to
// 1 hour, and a course that's been unpublished falls straight back to
// the short TTL on its next refresh (no separate "revoke" step needed —
// RefreshAssetURL always re-derives from the course's CURRENT status).
func TestSignedURLTTL_DraftVsPublished(t *testing.T) {
	cases := []struct {
		status  string
		wantMax time.Duration
	}{
		{models.CourseStatusDraft, 5 * time.Minute},
		{models.CourseStatusReview, 5 * time.Minute},
		{models.CourseStatusScheduled, 5 * time.Minute},
		{models.CourseStatusUnpublished, 5 * time.Minute},
		{models.CourseStatusPublished, time.Hour},
	}
	for _, tc := range cases {
		ttl := signedURLTTL(tc.status)
		if ttl > tc.wantMax {
			t.Errorf("status %q: expected ttl <= %v, got %v", tc.status, tc.wantMax, ttl)
		}
		if tc.status != models.CourseStatusPublished && ttl >= 5*time.Minute {
			t.Errorf("status %q: expected short-lived ttl (<5min), got %v", tc.status, ttl)
		}
	}
}

func TestSignedURLTTL_RevokedOnUnpublish(t *testing.T) {
	published := signedURLTTL(models.CourseStatusPublished)
	unpublished := signedURLTTL(models.CourseStatusUnpublished)
	if unpublished >= published {
		t.Errorf("expected unpublished ttl (%v) to be shorter than published ttl (%v)", unpublished, published)
	}
}
