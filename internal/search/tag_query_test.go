package search

import (
	"slices"
	"testing"
)

func TestTagCloudQueryOperations(t *testing.T) {
	if names := TagNames("alpha or (#Unicode not #Cities)", false); !slices.Equal(names, []string{"unicode", "cities"}) {
		t.Fatalf("selected tag names: %v", names)
	}
	if names := TagNames("alpha or #Unicode", true); !slices.Equal(names, []string{"alpha", "unicode"}) {
		t.Fatalf("lax selected tag names: %v", names)
	}
	if got := StripTag("#Unicode", "Unicode", false, false); got != "" {
		t.Fatalf("remove sole tag: %q", got)
	}
	if got := StripTag("alpha or (#Unicode #Cities)", "Unicode", false, false); got != "alpha or #Cities" {
		t.Fatalf("remove nested tag: %q", got)
	}
	if got := StripTag("alpha #Cities", "alpha", true, true); got != "#Cities" {
		t.Fatalf("legacy lax tag removal: %q", got)
	}
	if !IsTopLevelOr("alpha or #Unicode") || IsTopLevelOr("alpha #Unicode") {
		t.Fatal("top-level OR recognition")
	}
}
