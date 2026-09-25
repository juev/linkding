package importexport

import (
	"testing"
)

func TestParseNetscapeFlagsNotesFoldersAndEntities(t *testing.T) {
	source := `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<DL><p><DT><H3>Folder</H3><DL><p>
<DT><A HREF="https://example.com/a?x=1&amp;y=2" ADD_DATE="1" LAST_MODIFIED="11" PRIVATE="0" TOREAD="1" TAGS="Go,linkding:bookmarks.archived">A &amp; B</A>
<DD>Desc &lt;test&gt;[linkding-notes]Note &amp; more[/linkding-notes]
</DL><p><DT><A HREF="https://example.com/b" ADD_DATE="2">B</A>
</DL><p>`
	items, err := ParseNetscape(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("bookmarks: %+v", items)
	}
	first := items[0]
	if first.Href != "https://example.com/a?x=1&y=2" || first.Title != "A & B" || first.Description != "Desc <test>" || first.Notes != "Note & more" ||
		first.DateAdded != "1" || first.DateModified != "11" || first.Private || !first.ToRead || !first.Archived || len(first.TagNames) != 1 || first.TagNames[0] != "Go" {
		t.Fatalf("first parsed bookmark: %+v", first)
	}
	if items[1].Href != "https://example.com/b" || items[1].Title != "B" || !items[1].Private || items[1].Archived || items[1].Notes != "" {
		t.Fatalf("second parsed bookmark: %+v", items[1])
	}
}
