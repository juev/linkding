package markdown

import (
	"bytes"
	stdhtml "html"
	"html/template"
	"io"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	xhtml "golang.org/x/net/html"
)

var parser = goldmark.New(
	goldmark.WithExtensions(extension.Linkify),
	goldmark.WithRendererOptions(html.WithHardWraps(), html.WithUnsafe()),
)

var sanitizer = newPolicy()
var allowedTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"b": true, "i": true, "strong": true, "em": true, "tt": true, "p": true,
	"br": true, "span": true, "div": true, "blockquote": true, "code": true,
	"pre": true, "hr": true, "ul": true, "ol": true, "li": true, "dd": true,
	"dt": true, "img": true, "a": true, "sub": true, "sup": true,
}

func newPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("h1", "h2", "h3", "h4", "h5", "h6", "b", "i", "strong", "em", "tt", "p", "br", "span", "div", "blockquote", "code", "pre", "hr", "ul", "ol", "li", "dd", "dt", "img", "a", "sub", "sup")
	policy.AllowAttrs("id").Globally()
	policy.AllowAttrs("src", "alt", "title").OnElements("img")
	policy.AllowAttrs("href", "alt", "title").OnElements("a")
	policy.RequireParseableURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.RequireNoFollowOnLinks(true)
	return policy
}

// Render returns sanitized bookmark notes HTML for use in html/template.
func Render(source string) template.HTML {
	var rendered bytes.Buffer
	if err := parser.Convert([]byte(source), &rendered); err != nil {
		return ""
	}
	return template.HTML(sanitizer.SanitizeBytes(escapeDisallowedTags(rendered.Bytes())))
}

// Bleach escapes forbidden tag markup while leaving its text visible. Bluemonday
// removes some forbidden elements with their contents, so escape them first.
func escapeDisallowedTags(source []byte) []byte {
	tokens := xhtml.NewTokenizer(bytes.NewReader(source))
	var result bytes.Buffer
	for {
		tokenType := tokens.Next()
		if tokenType == xhtml.ErrorToken {
			if tokens.Err() != io.EOF {
				return source
			}
			return result.Bytes()
		}
		raw := tokens.Raw()
		if tokenType == xhtml.StartTagToken || tokenType == xhtml.EndTagToken || tokenType == xhtml.SelfClosingTagToken {
			name, _ := tokens.TagName()
			if !allowedTags[string(name)] {
				result.WriteString(stdhtml.EscapeString(string(raw)))
				continue
			}
		}
		result.Write(raw)
	}
}
