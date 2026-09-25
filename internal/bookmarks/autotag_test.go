package bookmarks

import (
	"slices"
	"testing"
)

func TestAutoTagsPinnedRules(t *testing.T) {
	cases := []struct {
		script, url string
		want        []string
	}{
		{"example.com example\ntest.com test", "https://example.com/", []string{"example"}},
		{"example.com example", "example.com", nil},
		{"example.com example\ntest.example.com test", "https://test.example.com/", []string{"example", "test"}},
		{"https://example.com/ https\nhttp://example.com/ http", "http://example.com/", []string{"http", "https"}},
		{"रजिस्ट्री.भारत tag1", "https://www.xn--81bg3cc2b2bk5hb.xn--h2brj9c/", []string{"tag1"}},
		{"example.com/page?a=b tag1 # comment\nexample.com/page?a= tag2\nexample.com/page?b= tag3", "https://example.com/page/some?a=b", []string{"tag1", "tag2"}},
		{"example.com/#/section section", "https://example.com/#/section/1", []string{"section"}},
	}
	for _, tc := range cases {
		got := AutoTags(tc.script, tc.url)
		if !slices.Equal(got, tc.want) {
			t.Errorf("AutoTags(%q, %q)=%q, want %q", tc.script, tc.url, got, tc.want)
		}
	}
}
