package httpserver

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/bookmarks"
	"github.com/juev/linkding/internal/config"
)

func serveFeed(w http.ResponseWriter, r *http.Request, root string, cfg config.Config, db *sql.DB, users *auth.Repository) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeNotFound(w, r)
		return
	}
	part := strings.TrimPrefix(r.URL.Path, root)
	public := part == "shared"
	feedKind := "public"
	key := ""
	if !public {
		segments := strings.Split(part, "/")
		if len(segments) != 2 || segments[0] == "" || (segments[1] != "all" && segments[1] != "unread" && segments[1] != "shared") {
			writeNotFound(w, r)
			return
		}
		key, feedKind = segments[0], segments[1]
	}
	ownerID := int64(0)
	if !public {
		query := "SELECT user_id FROM bookmarks_feedtoken WHERE key = " + assetMarker(cfg.DBEngine, 1)
		if err := db.QueryRowContext(r.Context(), query, key).Scan(&ownerID); err != nil {
			writeNotFound(w, r)
			return
		}
	}
	values := r.URL.Query()
	limit := 100
	if raw, exists := values["limit"]; exists && len(raw) > 0 {
		if raw[0] == "" {
			limit = 1_000_000
		} else if parsed, err := strconv.Atoi(raw[0]); err == nil && parsed > 0 {
			limit = parsed
		} else {
			http.Error(w, "Invalid feed limit", http.StatusBadRequest)
			return
		}
	}
	var sessionUser auth.User
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		sessionUser, _ = users.AuthenticateSession(r.Context(), cookie.Value)
	}
	if bundleID, err := strconv.ParseInt(values.Get("bundle"), 10, 64); err == nil && bundleID > 0 {
		query := "SELECT owner_id FROM bookmarks_bookmarkbundle WHERE id = " + assetMarker(cfg.DBEngine, 1)
		var bundleOwner int64
		if err := db.QueryRowContext(r.Context(), query, bundleID).Scan(&bundleOwner); err != nil || sessionUser.ID == 0 || sessionUser.ID != bundleOwner {
			writeNotFound(w, r)
			return
		}
	}
	opts := bookmarks.ListOptions{Query: values.Get("q"), User: values.Get("user"), Unread: values.Get("unread"),
		Shared: values.Get("shared"), Bundle: values.Get("bundle"), Limit: limit}
	if feedKind == "unread" {
		opts.Unread = "yes"
	}
	repo := bookmarks.NewRepository(db, cfg.DBEngine)
	var items []bookmarks.Bookmark
	var err error
	switch feedKind {
	case "all", "unread":
		items, _, err = repo.ListFiltered(r.Context(), ownerID, opts)
	case "shared":
		if opts.User != "" {
			query := "SELECT 1 FROM auth_user WHERE username = " + assetMarker(cfg.DBEngine, 1)
			var found int
			if lookupErr := db.QueryRowContext(r.Context(), query, opts.User).Scan(&found); errors.Is(lookupErr, sql.ErrNoRows) {
				items = []bookmarks.Bookmark{}
				break
			} else if lookupErr != nil {
				err = lookupErr
				break
			}
		}
		items, _, err = repo.ListShared(r.Context(), ownerID, true, opts)
	case "public":
		items, _, err = repo.ListPublicFeed(r.Context(), opts)
	}
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	title, description := "All bookmarks", "All bookmarks"
	switch feedKind {
	case "unread":
		title, description = "Unread bookmarks", "All unread bookmarks"
	case "shared":
		title, description = "Shared bookmarks", "All shared bookmarks"
	case "public":
		title, description = "Public shared bookmarks", "All public shared bookmarks"
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	link := scheme + "://" + r.Host + r.URL.Path
	last := time.Now().UTC()
	if len(items) > 0 {
		last = items[0].DateAdded.UTC()
		for _, item := range items[1:] {
			if item.DateAdded.After(last) {
				last = item.DateAdded.UTC()
			}
		}
	}
	body := renderRSS(title, description, link, last, items)
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Last-Modified", last.Format(http.TimeFormat))
	w.Header().Set("Vary", "Accept-Language, Cookie")
	w.Header().Set("Content-Language", selectedAdminLanguage(r).Code)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func renderRSS(title, description, link string, last time.Time, items []bookmarks.Bookmark) []byte {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	out.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel><title>`)
	out.WriteString(xmlFeedText(title))
	out.WriteString(`</title><link>`)
	out.WriteString(xmlFeedText(link))
	out.WriteString(`</link><description>`)
	out.WriteString(xmlFeedText(description))
	out.WriteString(`</description><atom:link href="`)
	out.WriteString(xmlFeedAttribute(link))
	out.WriteString(`" rel="self"/><language>en</language><lastBuildDate>`)
	out.WriteString(last.UTC().Format(time.RFC1123Z))
	out.WriteString(`</lastBuildDate>`)
	for _, item := range items {
		name := item.Title
		if name == "" {
			name = item.URL
		}
		out.WriteString(`<item><title>`)
		out.WriteString(xmlFeedText(name))
		out.WriteString(`</title><link>`)
		out.WriteString(xmlFeedText(item.URL))
		out.WriteString(`</link>`)
		if item.Description == "" {
			out.WriteString(`<description/>`)
		} else {
			out.WriteString(`<description>`)
			out.WriteString(xmlFeedText(item.Description))
			out.WriteString(`</description>`)
		}
		out.WriteString(`<pubDate>`)
		out.WriteString(item.DateAdded.UTC().Format(time.RFC1123Z))
		out.WriteString(`</pubDate><guid>`)
		out.WriteString(xmlFeedText(item.URL))
		out.WriteString(`</guid>`)
		for _, tag := range item.TagNames {
			out.WriteString(`<category>`)
			out.WriteString(xmlFeedText(tag))
			out.WriteString(`</category>`)
		}
		out.WriteString(`</item>`)
	}
	out.WriteString(`</channel></rss>`)
	return []byte(out.String())
}

func xmlFeedText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(sanitizeFeedText(value)))
	return out.String()
}

func xmlFeedAttribute(value string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;", "'", "&apos;").Replace(sanitizeFeedText(value))
}

func sanitizeFeedText(value string) string {
	return strings.Map(func(char rune) rune {
		if char == '\n' || char == '\r' || char == '\t' {
			return char
		}
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) || unicode.Is(unicode.Cs, char) || unicode.Is(unicode.Co, char) {
			return -1
		}
		return char
	}, value)
}
