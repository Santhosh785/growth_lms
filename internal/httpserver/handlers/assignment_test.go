package handlers

import (
	"testing"
	"time"

	"growth-lms/internal/models"
)

func TestComputeDueDateStatus(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		dueDate *time.Time
		want    string
	}{
		{
			name:    "no due date is always on_time",
			dueDate: nil,
			want:    models.DueDateStatusOnTime,
		},
		{
			name:    "due date in the future is on_time",
			dueDate: timePtr(now.Add(1 * time.Hour)),
			want:    models.DueDateStatusOnTime,
		},
		{
			name:    "due date in the past is late",
			dueDate: timePtr(now.Add(-1 * time.Hour)),
			want:    models.DueDateStatusLate,
		},
		{
			name:    "due date exactly now is on_time (not after)",
			dueDate: timePtr(now),
			want:    models.DueDateStatusOnTime,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDueDateStatus(now, tt.dueDate)
			if got != tt.want {
				t.Errorf("computeDueDateStatus(%v, %v) = %q, want %q", now, tt.dueDate, got, tt.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
