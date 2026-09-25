package httpserver

import "testing"

func TestAdminChangeMessageMatchesDjangoJSON(t *testing.T) {
	if got := adminChangeMessage(nil); got != "[]" {
		t.Fatalf("empty change: %q", got)
	}
	if got := adminChangeMessage([]string{"Name", "Date added"}); got != `[{"changed": {"fields": ["Name", "Date added"]}}]` {
		t.Fatalf("changed fields: %q", got)
	}
}
