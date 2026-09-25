package migratefrom

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// verifyReferencedFiles blocks migration when a bookmark points to a file
// missing from the upstream data directory.
func verifyReferencedFiles(ctx context.Context, db *sql.DB, sourceDir string) error {
	roots := make(map[string]*os.Root)
	defer func() {
		for _, root := range roots {
			root.Close()
		}
	}()
	check := func(folder, name string) error {
		if name == "" {
			return nil
		}
		root := roots[folder]
		if root == nil {
			var err error
			root, err = os.OpenRoot(filepath.Join(sourceDir, folder))
			if err != nil {
				return fmt.Errorf("open source %s directory: %w", folder, err)
			}
			roots[folder] = root
		}
		info, err := root.Stat(name)
		if err != nil {
			return fmt.Errorf("missing source %s file %q: %w", folder, name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source %s file %q is not a regular file", folder, name)
		}
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT favicon_file, preview_image_file FROM bookmarks_bookmark`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var favicon, preview string
		if err := rows.Scan(&favicon, &preview); err != nil {
			rows.Close()
			return err
		}
		if err := check("favicons", favicon); err != nil {
			rows.Close()
			return err
		}
		if err := check("previews", preview); err != nil {
			rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, `SELECT file FROM bookmarks_bookmarkasset`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return err
		}
		if err := check("assets", file); err != nil {
			return err
		}
	}
	return rows.Err()
}
