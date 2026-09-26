package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

func populateAdminFacetCounts(ctx context.Context, db *sql.DB, engine, model, search string, data *adminPageData) error {
	count := func(option *adminUserFilter) error {
		params, err := url.ParseQuery(strings.TrimPrefix(option.URL, "?"))
		if err != nil {
			return err
		}
		var query string
		var args []any
		switch model {
		case "user":
			query, args = adminUserListQuery(engine, search, params)
		case "bookmark":
			query, args = adminBookmarkListQuery(engine, search, params)
		case "bookmarkasset":
			query, args = adminAssetListQuery(engine, search, params.Get("status"))
		case "tag":
			query, args = adminTagListQuery(engine, search, params.Get("owner__username"))
		case "bookmarkbundle":
			query, args = adminBundleListQuery(engine, search, params.Get("owner__username"))
		case "apitoken":
			query, args = adminAPITokenListQuery(engine, search, params.Get("user__username"))
		case "feedtoken":
			query, args = adminFeedTokenListQuery(engine, search, params.Get("user__username"))
		case "toast":
			query, args = adminToastListQuery(engine, search, params.Get("owner__username"))
		default:
			return fmt.Errorf("unsupported admin facet model %q", model)
		}
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+`) AS facet_records`, args...).Scan(&option.Count)
	}
	for i := range data.ListFilters {
		for j := range data.ListFilters[i].Options {
			if err := count(&data.ListFilters[i].Options[j]); err != nil {
				return err
			}
		}
	}
	for i := range data.UserFilters {
		if err := count(&data.UserFilters[i]); err != nil {
			return err
		}
	}
	return nil
}

func adminClearFiltersURL(current url.Values, data adminPageData) (bool, string) {
	params := url.Values{}
	for name, values := range current {
		if name != "p" {
			params[name] = append([]string(nil), values...)
		}
	}
	active := false
	for _, group := range data.ListFilters {
		if params.Has(group.Param) || group.ExtraParam != "" && params.Has(group.ExtraParam) {
			active = true
			params.Del(group.Param)
			params.Del(group.ExtraParam)
		}
	}
	if data.UserFilterParam != "" && params.Has(data.UserFilterParam) {
		active = true
		params.Del(data.UserFilterParam)
	}
	if encoded := params.Encode(); encoded != "" {
		return active, "?" + encoded
	}
	return active, "?"
}
