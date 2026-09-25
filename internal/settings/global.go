package settings

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strconv"
)

type Global struct {
	ID                 int64
	LandingPage        string
	GuestProfileUserID sql.NullInt64
	EnableLinkPrefetch bool
}

type UserOption struct {
	ID       int64
	Username string
}

func LoadGlobal(ctx context.Context, db *sql.DB) (Global, error) {
	var value Global
	err := db.QueryRowContext(ctx, `SELECT id, landing_page, guest_profile_user_id, enable_link_prefetch
		FROM bookmarks_globalsettings ORDER BY id LIMIT 1`).Scan(&value.ID, &value.LandingPage,
		&value.GuestProfileUserID, &value.EnableLinkPrefetch)
	if errors.Is(err, sql.ErrNoRows) {
		return Global{LandingPage: "login"}, nil
	}
	return value, err
}

func ListUsers(ctx context.Context, db *sql.DB) ([]UserOption, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, username FROM auth_user ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []UserOption
	for rows.Next() {
		var item UserOption
		if err := rows.Scan(&item.ID, &item.Username); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func UpdateGlobal(ctx context.Context, db *sql.DB, engine string, isSuperuser bool, form url.Values) error {
	if !isSuperuser {
		return errors.New("global settings require superuser")
	}
	landing := form.Get("landing_page")
	if landing != "login" && landing != "shared_bookmarks" {
		return ValidationError{Field: "landing_page"}
	}
	var guest any
	if raw := form.Get("guest_profile_user"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return ValidationError{Field: "guest_profile_user"}
		}
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_user WHERE id = `+marker(engine, 1), id).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return ValidationError{Field: "guest_profile_user"}
		}
		guest = id
	}
	prefetch := form.Get("enable_link_prefetch") != ""
	current, err := LoadGlobal(ctx, db)
	if err != nil {
		return err
	}
	if current.ID == 0 {
		query := `INSERT INTO bookmarks_globalsettings (landing_page, guest_profile_user_id, enable_link_prefetch)
			VALUES (` + marker(engine, 1) + `,` + marker(engine, 2) + `,` + marker(engine, 3) + `)`
		_, err = db.ExecContext(ctx, query, landing, guest, prefetch)
	} else {
		query := `UPDATE bookmarks_globalsettings SET landing_page = ` + marker(engine, 1) +
			`, guest_profile_user_id = ` + marker(engine, 2) + `, enable_link_prefetch = ` + marker(engine, 3) +
			` WHERE id = ` + marker(engine, 4)
		_, err = db.ExecContext(ctx, query, landing, guest, prefetch, current.ID)
	}
	return err
}
