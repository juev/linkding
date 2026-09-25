package importexport

import (
	"io"
	"strings"

	"github.com/juev/linkding/internal/bookmarks"
	"golang.org/x/net/html"
)

type NetscapeBookmark struct {
	Href         string
	Normalized   string
	Title        string
	Description  string
	Notes        string
	DateAdded    string
	DateModified string
	TagNames     []string
	ToRead       bool
	Private      bool
	Archived     bool
}

// ParseNetscape follows the v1.47.0 HTMLParser state machine, including its
// acceptance of incomplete Netscape bookmark markup.
func ParseNetscape(source string) ([]NetscapeBookmark, error) {
	var result []NetscapeBookmark
	var current *NetscapeBookmark
	currentTag := ""
	var title, description, notes string
	flush := func() {
		if current != nil {
			current.Title = title
			current.Description = description
			current.Notes = notes
			result = append(result, *current)
		}
		current = nil
		title, description, notes = "", "", ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if err := tokenizer.Err(); err != io.EOF {
				return nil, err
			}
			break
		}
		token := tokenizer.Token()
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			tag := strings.ToLower(token.Data)
			if tag == "dt" {
				flush()
			}
			if tag == "a" {
				attributes := make(map[string]string, len(token.Attr))
				for _, attr := range token.Attr {
					attributes[strings.ToLower(attr.Key)] = attr.Val
				}
				href := attributes["href"]
				tags := attributes["tags"]
				tagNames := bookmarks.ParseTagString(tags, ",")
				archived := strings.Contains(tags, "linkding:bookmarks.archived")
				for index, name := range tagNames {
					if name == "linkding:bookmarks.archived" {
						tagNames = append(tagNames[:index], tagNames[index+1:]...)
						break
					}
				}
				current = &NetscapeBookmark{
					Href: href, Normalized: bookmarks.NormalizeURL(href),
					DateAdded: attributes["add_date"], DateModified: attributes["last_modified"],
					TagNames: tagNames, ToRead: attributes["toread"] == "1",
					Private: attributes["private"] != "0", Archived: archived,
				}
			}
			currentTag = tag
		case html.EndTagToken:
			if strings.ToLower(token.Data) == "dl" {
				flush()
			}
			currentTag = ""
		case html.TextToken:
			data := strings.TrimSpace(token.Data)
			switch currentTag {
			case "a":
				title = data
			case "dd":
				if before, after, found := strings.Cut(data, "[linkding-notes]"); found {
					description = before
					notes, _, _ = strings.Cut(after, "[/linkding-notes]")
				} else {
					description = data
				}
			}
		}
	}
	return result, nil
}
