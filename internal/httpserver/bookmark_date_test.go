package httpserver

import (
	"testing"
	"time"
)

func TestBookmarkDateLabelsMatchPinnedPython(t *testing.T) {
	now := time.Date(2026, 9, 25, 19, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		date, relative, absolute string
	}{
		{"2026-09-25", "Today", "Today"},
		{"2026-09-24", "Yesterday", "Yesterday"},
		{"2026-09-22", "Tuesday", "Tuesday"},
		{"2026-09-18", "1 week ago", "09/18/2026"},
		{"2026-09-10", "2 weeks ago", "09/10/2026"},
		{"2026-08-25", "1 month ago", "08/25/2026"},
		{"2025-09-25", "1 year ago", "09/25/2025"},
		{"2024-09-24", "2 years ago", "09/24/2024"},
	} {
		value, err := time.Parse("2006-01-02", tc.date)
		if err != nil {
			t.Fatal(err)
		}
		value = value.Add(10 * time.Hour)
		if got := relativeBookmarkDate(value, now); got != tc.relative {
			t.Errorf("relative %s = %q, want %q", tc.date, got, tc.relative)
		}
		if got := absoluteBookmarkDate(value, now); got != tc.absolute {
			t.Errorf("absolute %s = %q, want %q", tc.date, got, tc.absolute)
		}
	}
}
