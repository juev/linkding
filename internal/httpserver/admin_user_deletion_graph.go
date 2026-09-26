package httpserver

import (
	"context"
	"database/sql"
	"net/url"
	"strconv"
)

type adminDeletionSummary struct {
	Label string
	Count int
}

type adminDeletionNode struct {
	ID, Label, Repr, URL string
	Children             []adminDeletionNode
}

func adminUserDeletionGraph(ctx context.Context, db *sql.DB, engine, prefix string, users []adminUserSelected) ([]adminDeletionSummary, []adminDeletionNode, error) {
	counts := map[string]int{"Users": len(users)}
	var roots []adminDeletionNode
	seenRelationships := make(map[int64]bool)
	for _, user := range users {
		root := adminDeletionNode{Label: "User", Repr: user.Username, URL: prefix + "admin/auth/user/" + strconv.FormatInt(user.ID, 10) + "/change/"}
		marker := assetMarker(engine, 1)
		tags, err := adminNumericDeletionNodes(ctx, db, prefix, "Tag", "tag", `SELECT id,name FROM bookmarks_tag WHERE owner_id=`+marker+` ORDER BY id`, user.ID)
		if err != nil {
			return nil, nil, err
		}
		for i := range tags {
			tagID, _ := strconv.ParseInt(tags[i].ID, 10, 64)
			relations, ids, err := adminBookmarkTagDeletionNodes(ctx, db, engine, "tag_id", tagID, seenRelationships)
			if err != nil {
				return nil, nil, err
			}
			tags[i].Children = relations
			counts["Bookmark-tag relationships"] += len(ids)
		}
		counts["Tags"] += len(tags)
		root.Children = append(root.Children, tags...)

		bookmarks, err := adminNumericDeletionNodes(ctx, db, prefix, "Bookmark", "bookmark", `SELECT id,COALESCE(NULLIF(title,''),url) || ' (' || SUBSTR(url,1,30) || '...)' FROM bookmarks_bookmark WHERE owner_id=`+marker+` ORDER BY id`, user.ID)
		if err != nil {
			return nil, nil, err
		}
		for i := range bookmarks {
			bookmarkID, _ := strconv.ParseInt(bookmarks[i].ID, 10, 64)
			relations, ids, err := adminBookmarkTagDeletionNodes(ctx, db, engine, "bookmark_id", bookmarkID, seenRelationships)
			if err != nil {
				return nil, nil, err
			}
			bookmarks[i].Children = append(bookmarks[i].Children, relations...)
			counts["Bookmark-tag relationships"] += len(ids)
			assets, err := adminNumericDeletionNodes(ctx, db, prefix, "Bookmark asset", "bookmarkasset", `SELECT id,COALESCE(NULLIF(display_name,''),'Bookmark Asset #' || id) FROM bookmarks_bookmarkasset WHERE bookmark_id=`+marker+` ORDER BY id`, bookmarkID)
			if err != nil {
				return nil, nil, err
			}
			bookmarks[i].Children = append(bookmarks[i].Children, assets...)
			counts["Bookmark assets"] += len(assets)
		}
		counts["Bookmarks"] += len(bookmarks)
		root.Children = append(root.Children, bookmarks...)

		for _, group := range []struct {
			label, model, summary, query string
		}{
			{"Bookmark bundle", "bookmarkbundle", "Bookmark bundles", `SELECT id,name FROM bookmarks_bookmarkbundle WHERE owner_id=` + marker + ` ORDER BY id`},
			{"User profile", "", "User profiles", `SELECT id,'UserProfile object (' || id || ')' FROM bookmarks_userprofile WHERE user_id=` + marker + ` ORDER BY id`},
			{"Toast", "toast", "Toasts", `SELECT id,'Toast object (' || id || ')' FROM bookmarks_toast WHERE owner_id=` + marker + ` ORDER BY id`},
		} {
			nodes, err := adminNumericDeletionNodes(ctx, db, prefix, group.label, group.model, group.query, user.ID)
			if err != nil {
				return nil, nil, err
			}
			root.Children = append(root.Children, nodes...)
			counts[group.summary] += len(nodes)
		}
		feedTokens, err := adminStringDeletionNodes(ctx, db, prefix, "Feed token", "feedtoken", `SELECT key,key FROM bookmarks_feedtoken WHERE user_id=`+marker+` ORDER BY key`, user.ID)
		if err != nil {
			return nil, nil, err
		}
		root.Children = append(root.Children, feedTokens...)
		counts["Feed tokens"] += len(feedTokens)
		apiTokens, err := adminNumericDeletionNodes(ctx, db, prefix, "Api token", "apitoken", `SELECT t.id,t.name || ' (' || u.username || ')' FROM bookmarks_apitoken AS t JOIN auth_user AS u ON u.id=t.user_id WHERE t.user_id=`+marker+` ORDER BY t.id`, user.ID)
		if err != nil {
			return nil, nil, err
		}
		root.Children = append(root.Children, apiTokens...)
		counts["Api tokens"] += len(apiTokens)
		logs, err := adminUserDeletionLogs(ctx, db, engine, user.ID)
		if err != nil {
			return nil, nil, err
		}
		root.Children = append(root.Children, logs...)
		counts["Log entries"] += len(logs)
		roots = append(roots, root)
	}
	var summary []adminDeletionSummary
	for _, label := range []string{"Users", "Tags", "Bookmark-tag relationships", "Bookmarks", "Bookmark assets", "Bookmark bundles", "User profiles", "Toasts", "Feed tokens", "Api tokens", "Log entries"} {
		if counts[label] > 0 {
			summary = append(summary, adminDeletionSummary{Label: label, Count: counts[label]})
		}
	}
	return summary, roots, nil
}

func adminNumericDeletionNodes(ctx context.Context, db *sql.DB, prefix, label, model, query string, arg any) ([]adminDeletionNode, error) {
	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []adminDeletionNode
	for rows.Next() {
		var id int64
		var repr string
		if err := rows.Scan(&id, &repr); err != nil {
			return nil, err
		}
		node := adminDeletionNode{ID: strconv.FormatInt(id, 10), Label: label, Repr: repr}
		if model != "" {
			node.URL = prefix + "admin/bookmarks/" + model + "/" + node.ID + "/change/"
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func adminStringDeletionNodes(ctx context.Context, db *sql.DB, prefix, label, model, query string, arg any) ([]adminDeletionNode, error) {
	rows, err := db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []adminDeletionNode
	for rows.Next() {
		var id, repr string
		if err := rows.Scan(&id, &repr); err != nil {
			return nil, err
		}
		nodes = append(nodes, adminDeletionNode{ID: id, Label: label, Repr: repr, URL: prefix + "admin/bookmarks/" + model + "/" + url.PathEscape(id) + "/change/"})
	}
	return nodes, rows.Err()
}

func adminBookmarkTagDeletionNodes(ctx context.Context, db *sql.DB, engine, column string, id int64, seen map[int64]bool) ([]adminDeletionNode, []int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM bookmarks_bookmark_tags WHERE `+column+`=`+assetMarker(engine, 1)+` ORDER BY id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var nodes []adminDeletionNode
	var ids []int64
	for rows.Next() {
		var relationID int64
		if err := rows.Scan(&relationID); err != nil {
			return nil, nil, err
		}
		if seen[relationID] {
			continue
		}
		seen[relationID] = true
		ids = append(ids, relationID)
		nodes = append(nodes, adminDeletionNode{Label: "Bookmark-tag relationship", Repr: "Bookmark_tags object (" + strconv.FormatInt(relationID, 10) + ")"})
	}
	return nodes, ids, rows.Err()
}

func adminUserDeletionLogs(ctx context.Context, db *sql.DB, engine string, userID int64) ([]adminDeletionNode, error) {
	rows, err := db.QueryContext(ctx, `SELECT object_repr,action_flag FROM django_admin_log WHERE user_id=`+assetMarker(engine, 1)+` ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []adminDeletionNode
	for rows.Next() {
		var repr string
		var flag int
		if err := rows.Scan(&repr, &flag); err != nil {
			return nil, err
		}
		verb := "Changed"
		if flag == 1 {
			verb = "Added"
		} else if flag == 3 {
			verb = "Deleted"
		}
		nodes = append(nodes, adminDeletionNode{Label: "Log entry", Repr: verb + " “" + repr + "”."})
	}
	return nodes, rows.Err()
}
