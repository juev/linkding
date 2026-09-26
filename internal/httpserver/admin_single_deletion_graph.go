package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type adminSingleDeletion struct {
	Model, Repr, ChangeURL string
	Summary                []adminDeletionSummary
	Nodes                  []adminDeletionNode
}

func adminSingleDeletionGraph(ctx context.Context, db *sql.DB, engine, prefix, model, id, repr string) (adminSingleDeletion, error) {
	labels := map[string]struct{ singular, plural string }{
		"toast":          {"Toast", "Toasts"},
		"bookmark":       {"Bookmark", "Bookmarks"},
		"bookmarkbundle": {"Bookmark bundle", "Bookmark bundles"},
		"bookmarkasset":  {"Bookmark asset", "Bookmark assets"},
		"apitoken":       {"Api token", "Api tokens"},
		"feedtoken":      {"Feed token", "Feed tokens"},
	}
	label, ok := labels[model]
	if !ok {
		return adminSingleDeletion{}, fmt.Errorf("unsupported Admin deletion model %q", model)
	}
	changeURL := prefix + "admin/bookmarks/" + model + "/" + url.PathEscape(id) + "/change/"
	data := adminSingleDeletion{
		Model:     strings.ToLower(label.singular),
		Repr:      repr,
		ChangeURL: changeURL,
		Summary:   []adminDeletionSummary{{Label: label.plural, Count: 1}},
		Nodes:     []adminDeletionNode{{Label: label.singular, Repr: repr, URL: changeURL}},
	}
	if model != "bookmark" {
		return data, nil
	}
	bookmarkID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return adminSingleDeletion{}, err
	}
	relations, relationIDs, err := adminBookmarkTagDeletionNodes(ctx, db, engine, "bookmark_id", bookmarkID, make(map[int64]bool))
	if err != nil {
		return adminSingleDeletion{}, err
	}
	if len(relationIDs) > 0 {
		data.Summary = append(data.Summary, adminDeletionSummary{Label: "Bookmark-tag relationships", Count: len(relationIDs)})
		data.Nodes[0].Children = append(data.Nodes[0].Children, relations...)
	}
	assets, err := adminNumericDeletionNodes(ctx, db, prefix, "Bookmark asset", "bookmarkasset", `SELECT id,COALESCE(NULLIF(display_name,''),'Bookmark Asset #' || id) FROM bookmarks_bookmarkasset WHERE bookmark_id=`+assetMarker(engine, 1)+` ORDER BY id`, bookmarkID)
	if err != nil {
		return adminSingleDeletion{}, err
	}
	if len(assets) > 0 {
		data.Summary = append(data.Summary, adminDeletionSummary{Label: "Bookmark assets", Count: len(assets)})
		data.Nodes[0].Children = append(data.Nodes[0].Children, assets...)
	}
	return data, nil
}
