package httpserver

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
	"golang.org/x/net/html"
)

func serveBookmarkActionStream(w http.ResponseWriter, r *http.Request, cfg config.Config, db *sql.DB, users *auth.Repository, repo *bookmarks.Repository, archived, shared bool) {
	listPath := cfg.URLPrefix() + "bookmarks"
	if archived {
		listPath += "/archived"
	}
	if shared {
		listPath += "/shared"
	}
	listRequest := r.Clone(r.Context())
	listRequest.Method = http.MethodGet
	listURL := *r.URL
	listURL.Path = listPath
	listRequest.URL = &listURL
	listRequest.Body = nil
	listRequest.Header.Del("Turbo-Frame")
	capture := httptest.NewRecorder()
	serveBookmarkList(capture, listRequest, listPath, cfg, db, users, repo)
	if capture.Code != http.StatusOK {
		for key, values := range capture.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(capture.Code)
		_, _ = w.Write(capture.Body.Bytes())
		return
	}
	document, err := html.Parse(bytes.NewReader(capture.Body.Bytes()))
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	list := findHTMLNodeByID(document, "bookmark-list-container")
	tags := findHTMLNodeByID(document, "tag-cloud-container")
	details := findHTMLNodeByID(document, "details-modal")
	if list == nil || tags == nil || details == nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	var listHTML, tagsHTML, detailsHTML bytes.Buffer
	for child := list.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&listHTML, child); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
	}
	for child := tags.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&tagsHTML, child); err != nil {
			http.Error(w, "Server error", http.StatusInternalServerError)
			return
		}
	}
	if err := html.Render(&detailsHTML, details); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	listMarkup := strings.ReplaceAll(listHTML.String(), `href="`+listPath+`?page=`, `href="`+r.URL.Path+`?page=`)
	var stream bytes.Buffer
	stream.WriteString(`<turbo-stream action="update" target="bookmark-list-container"><template>`)
	stream.WriteString(listMarkup)
	stream.WriteString(`<script>document.dispatchEvent(new CustomEvent('bookmark-list-updated'));</script></template></turbo-stream>`)
	stream.WriteString(`<turbo-stream action="update" target="tag-cloud-container"><template>`)
	stream.Write(tagsHTML.Bytes())
	stream.WriteString(`</template></turbo-stream>`)
	stream.WriteString(`<turbo-stream action="replace" method="morph" target="details-modal"><template>`)
	stream.Write(detailsHTML.Bytes())
	stream.WriteString(`</template></turbo-stream>`)
	for _, key := range []string{"Content-Language", "Vary", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Cross-Origin-Opener-Policy"} {
		if value := capture.Header().Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.Header().Set("Content-Type", "text/vnd.turbo-stream.html")
	w.Header().Set("Content-Length", strconv.Itoa(stream.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stream.Bytes())
}

func findHTMLNodeByID(node *html.Node, id string) *html.Node {
	for _, attr := range node.Attr {
		if attr.Key == "id" && attr.Val == id {
			return node
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNodeByID(child, id); found != nil {
			return found
		}
	}
	return nil
}
