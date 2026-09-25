package markdown

import (
	"strings"
	"testing"
)

func TestRenderNotesMarkdownAndSanitizeHTML(t *testing.T) {
	actual := string(Render("**bold**\nnext\n\n[link](https://example.com) <script>alert(1)</script>"))
	if want := "<p><strong>bold</strong><br>\nnext</p>\n<p><a href=\"https://example.com\" rel=\"nofollow\">link</a> &lt;script&gt;alert(1)&lt;/script&gt;</p>\n"; actual != want {
		t.Fatalf("Django markdown mismatch:\n got %q\nwant %q", actual, want)
	}
	if !strings.Contains(actual, "<strong>bold</strong>") || !strings.Contains(actual, "<br>") || !strings.Contains(actual, `href="https://example.com"`) {
		t.Fatalf("missing markdown rendering: %q", actual)
	}
	if strings.Contains(actual, "<script") || strings.Contains(actual, "javascript:") {
		t.Fatalf("unsafe HTML retained: %q", actual)
	}
	unsafe := string(Render(`[bad](javascript:alert(1)) <img src="javascript:alert(1)" onerror="alert(1)">`))
	if strings.Contains(unsafe, "javascript:") || strings.Contains(unsafe, "onerror") {
		t.Fatalf("unsafe URL or attribute retained: %q", unsafe)
	}
}
