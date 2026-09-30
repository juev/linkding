package metadata

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/juev/linkding/internal/bookmarks"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

const maxPageBytes = 5_000 * 1024

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/101.0.0.0 Safari/537.36"

type Metadata struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	PreviewImage string `json:"preview_image"`
}

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Load mirrors the upstream best-effort metadata read. Failed or blocked
// requests leave fields empty while retaining the requested URL.
func Load(ctx context.Context, client Doer, pageURL string) Metadata {
	result := Metadata{URL: pageURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return result
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml")
	req.Header.Set("Dnt", "1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", userAgent)
	response, err := client.Do(req)
	if err != nil {
		return result
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+50*1024))
	if err != nil {
		return result
	}
	if end := bytes.Index(content, []byte("</head>")); end >= 0 {
		content = content[:end+len("</head>")]
	}
	// Valid multibyte UTF-8 takes precedence over a mislabeled charset. Pure
	// ASCII can still contain stateful encodings such as ISO-2022-JP.
	hasNonASCII := false
	for _, b := range content {
		if b >= utf8.RuneSelf {
			hasNonASCII = true
			break
		}
	}
	decoded := content
	if !utf8.Valid(content) || !hasNonASCII {
		selected, _, bom := charset.DetermineEncoding(content, "")
		if !bom {
			if declared := declaredHTMLEncoding(content); declared != nil {
				selected = declared
			} else {
				selected, _, _ = charset.DetermineEncoding(content, response.Header.Get("Content-Type"))
			}
		}
		decoded, err = io.ReadAll(selected.NewDecoder().Reader(bytes.NewReader(content)))
		if err != nil {
			return result
		}
	}
	doc, err := html.Parse(bytes.NewReader(decoded))
	if err != nil {
		return result
	}
	var ogDescription string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "title":
				if result.Title == "" {
					result.Title = bookmarks.NormalizeTitle(textContent(node))
				}
			case "meta":
				name := attribute(node, "name")
				property := attribute(node, "property")
				value := strings.TrimSpace(attribute(node, "content"))
				if name == "description" && result.Description == "" {
					result.Description = value
				}
				if property == "og:description" && ogDescription == "" {
					ogDescription = value
				}
				if property == "og:image" && result.PreviewImage == "" {
					result.PreviewImage = value
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if result.Description == "" {
		result.Description = ogDescription
	}
	if result.PreviewImage != "" && !strings.HasPrefix(result.PreviewImage, "http://") && !strings.HasPrefix(result.PreviewImage, "https://") {
		base, baseErr := url.Parse(pageURL)
		image, imageErr := url.Parse(result.PreviewImage)
		if baseErr == nil && imageErr == nil {
			result.PreviewImage = base.ResolveReference(image).String()
		}
	}
	return result
}

// DetermineEncoding scans only 1024 bytes, so inspect the full head for late declarations.
func declaredHTMLEncoding(content []byte) encoding.Encoding {
	tokenizer := html.NewTokenizer(bytes.NewReader(content))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return nil
		case html.EndTagToken:
			name, _ := tokenizer.TagName()
			if bytes.EqualFold(name, []byte("head")) {
				return nil
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			if !bytes.EqualFold(name, []byte("meta")) {
				continue
			}
			var label, httpEquiv, contentType string
			for hasAttr {
				key, value, more := tokenizer.TagAttr()
				hasAttr = more
				switch string(key) {
				case "charset":
					label = string(value)
				case "http-equiv":
					httpEquiv = string(value)
				case "content":
					contentType = string(value)
				}
			}
			if label == "" && strings.EqualFold(httpEquiv, "content-type") {
				_, params, err := mime.ParseMediaType(contentType)
				if err == nil {
					label = params["charset"]
				}
			}
			if selected, _ := charset.Lookup(strings.TrimSpace(label)); selected != nil {
				return selected
			}
		}
	}
}

func attribute(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func textContent(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			builder.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}
