package models_test

import (
	"testing"

	"growth-lms/internal/models"
)

func TestNextSortOrder(t *testing.T) {
	if got := models.NextSortOrder(nil); got != 1.0 {
		t.Errorf("expected 1.0 for empty list, got %v", got)
	}
	if got := models.NextSortOrder([]float64{1.0, 2.0}); got != 3.0 {
		t.Errorf("expected 3.0, got %v", got)
	}
}

func TestMidpointSortOrder(t *testing.T) {
	value, needsRenorm := models.MidpointSortOrder(1.0, 2.0)
	if needsRenorm {
		t.Fatal("expected no renormalization needed for a normal gap")
	}
	if value != 1.5 {
		t.Errorf("expected 1.5, got %v", value)
	}
}

func TestMidpointSortOrder_TriggersRenormalize(t *testing.T) {
	_, needsRenorm := models.MidpointSortOrder(1.0, 1.0000000001)
	if !needsRenorm {
		t.Fatal("expected renormalization to be required when precision is exhausted")
	}
}

func TestRenormalize(t *testing.T) {
	got := models.Renormalize(3)
	want := []float64{1.0, 2.0, 3.0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: expected %v, got %v", i, want[i], got[i])
		}
	}
}
