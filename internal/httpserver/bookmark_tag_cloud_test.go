package httpserver

import (
	"net/url"
	"strings"
	"testing"
)

func TestBookmarkTagCloudSelectionAndGrouping(t *testing.T) {
	selected, groups := buildListTagCloud(
		[]string{"Cities", "Greek", "Unicode"},
		[]string{"Unicode"},
		[]string{"unicode"},
		"q=%23Unicode",
		url.Values{"q": {"#Unicode"}},
		url.Values{"tag_grouping": {"alphabetical"}},
	)
	if len(selected) != 1 || selected[0].Name != "Unicode" || selected[0].Query != "q=" {
		t.Fatalf("selected tags: %+v", selected)
	}
	if len(groups) != 2 || groups[0].Tags[0].Name != "Cities" || groups[1].Tags[0].Name != "Greek" {
		t.Fatalf("alphabetical groups: %+v", groups)
	}
	if !groups[0].Tags[0].Highlight || groups[0].Tags[0].FirstChar != "C" || groups[0].Tags[0].Remaining != "ities" {
		t.Fatalf("first character highlight: %+v", groups[0].Tags[0])
	}
	if !strings.Contains(string(groups[0].Tags[0].Query), "q=%23Unicode+%23Cities") {
		t.Fatalf("add tag query: %s", groups[0].Tags[0].Query)
	}
	_, groups = buildListTagCloud([]string{"Cities", "Greek"}, nil, nil, "", url.Values{}, url.Values{"tag_grouping": {"disabled"}})
	if len(groups) != 1 || len(groups[0].Tags) != 2 || groups[0].Tags[0].Highlight {
		t.Fatalf("disabled grouping: %+v", groups)
	}
	_, groups = buildListTagCloud([]string{"Alice", "東京", "中文"}, nil, nil, "", url.Values{}, url.Values{"tag_grouping": {"alphabetical"}})
	if len(groups) != 2 || len(groups[1].Tags) != 2 || groups[1].Tags[0].Name != "中文" || groups[1].Tags[1].Name != "東京" {
		t.Fatalf("CJK tags must form a final group: %+v", groups)
	}
}

func TestOrderedListQueryMatchesQueryDictOrdering(t *testing.T) {
	raw := "q=%23Unicode&shared=yes&details=4"
	if got := pageQuery(raw, 2); got != "q=%23Unicode&shared=yes&page=2" {
		t.Fatalf("page link query: %q", got)
	}
	if got := orderedListQuery(raw, "details", "5", "page"); got != "q=%23Unicode&shared=yes&details=5" {
		t.Fatalf("details link query: %q", got)
	}
	if got := orderedListQuery(raw, "", "", "details"); got != "q=%23Unicode&shared=yes" {
		t.Fatalf("return URL query: %q", got)
	}
}
