package bookmarks

import "testing"

func TestNormalizeURLMatchesPinnedPython(t *testing.T) {
	// Output captured from bookmarks.utils.normalize_url in the v1.47.0 image.
	cases := []struct{ input, want string }{
		{" HTTPS://Example.COM/path///?b=2&a=1#Frag ", "https://example.com/path?a=1&b=2#Frag"},
		{"https://EXAMPLE.COM/", "https://example.com"},
		{"https://user:pass@EXAMPLE.COM:443/a%2Fb/?x=a+b&x=a%20b&empty=", "https://user:pass@example.com:443/a%2Fb?empty=&x=a%20b&x=a%20b"},
		{"https://[::1]/a/", "https://::1/a"},
		{"https://example.com/foo/;p?b=2&a=1", "https://example.com/foo;p?a=1&b=2"},
		{"https://example.com/x?z=%2f&z=%2F", "https://example.com/x?z=%2F&z=%2F"},
		{"mailto:FOO@Example.com", "mailto:FOO@Example.com"},
		{"ftp://EXAMPLE.COM/a;b///?z=2&a=1", "ftp://example.com/a;b?a=1&z=2"},
		{"example.com/path/?z=2", "example.com/path?z=2"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeURL(tc.input); got != tc.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
