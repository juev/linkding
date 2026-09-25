package metadata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
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
	encoding, _, _ := charset.DetermineEncoding(content, "")
	decoded, err := io.ReadAll(encoding.NewDecoder().Reader(bytes.NewReader(content)))
	if err != nil {
		return result
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
					result.Title = strings.TrimSpace(textContent(node))
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
