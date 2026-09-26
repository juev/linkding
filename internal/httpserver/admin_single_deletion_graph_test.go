package httpserver

import (
	"context"
	"strings"
	"testing"
)

func TestAdminSingleDeletionGraphSimpleModels(t *testing.T) {
	for _, tc := range []struct {
		model, id, label, plural string
	}{
		{"toast", "1", "Toast", "Toasts"},
		{"bookmarkbundle", "2", "Bookmark bundle", "Bookmark bundles"},
		{"bookmarkasset", "3", "Bookmark asset", "Bookmark assets"},
		{"apitoken", "4", "Api token", "Api tokens"},
		{"feedtoken", "special/key", "Feed token", "Feed tokens"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			data, err := adminSingleDeletionGraph(context.Background(), nil, "sqlite", "/linkding/", tc.model, tc.id, "example")
			if err != nil {
				t.Fatal(err)
			}
			if data.Model != strings.ToLower(tc.label) || data.Summary[0] != (adminDeletionSummary{Label: tc.plural, Count: 1}) || data.Nodes[0].Label != tc.label || data.Nodes[0].Repr != "example" || data.Nodes[0].URL != data.ChangeURL {
				t.Fatalf("deletion graph = %+v", data)
			}
			if !strings.HasPrefix(data.ChangeURL, "/linkding/admin/bookmarks/"+tc.model+"/") || !strings.HasSuffix(data.ChangeURL, "/change/") {
				t.Fatalf("change URL = %q", data.ChangeURL)
			}
			if tc.model == "feedtoken" && !strings.Contains(data.ChangeURL, "special%2Fkey") {
				t.Fatalf("feed token key is not path escaped: %q", data.ChangeURL)
			}
		})
	}
	if _, err := adminSingleDeletionGraph(context.Background(), nil, "sqlite", "/", "unknown", "1", "example"); err == nil {
		t.Fatal("unsupported Admin model accepted")
	}
}
