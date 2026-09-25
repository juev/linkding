package importexport

import (
	"strings"
	"testing"
	"time"

	"github.com/juev/linkding/internal/bookmarks"
)

func TestExportNetscapePreservesFlagsNotesTagsAndEscaping(t *testing.T) {
	first := bookmarks.Bookmark{URL: "https://example.com/1", Title: "Title 1", Description: "Example description",
		DateAdded: time.Unix(1, 0), DateModified: time.Unix(11, 0)}
	second := bookmarks.Bookmark{URL: "https://example.com/2", Title: "<Title>", Notes: "It's <good>",
		Unread: true, Shared: true, IsArchived: true, TagNames: []string{"tag2", `"`, "&"},
		DateAdded: time.Unix(2, 0), DateModified: time.Unix(22, 0)}
	output := ExportNetscape([]bookmarks.Bookmark{first, second})
	for _, expected := range []string{
		"<!DOCTYPE NETSCAPE-Bookmark-file-1>\n\r",
		`<DT><A HREF="https://example.com/1" ADD_DATE="1" LAST_MODIFIED="11" PRIVATE="1" TOREAD="0" TAGS="">Title 1</A>`,
		"<DD>Example description",
		`<DT><A HREF="https://example.com/2" ADD_DATE="2" LAST_MODIFIED="22" PRIVATE="0" TOREAD="1" TAGS="&quot;,&amp;,tag2,linkding:bookmarks.archived">&lt;Title&gt;</A>`,
		"<DD>[linkding-notes]It&#x27;s &lt;good&gt;[/linkding-notes]",
		"</DL><p>",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing %q in export: %s", expected, output)
		}
	}
	if strings.Count(output, "\n\r") != 9 {
		t.Fatalf("line separator count: %d", strings.Count(output, "\n\r"))
	}
}
