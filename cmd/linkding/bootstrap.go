package main

import (
	"context"
	"database/sql"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func ensureInitialSuperuser(ctx context.Context, db *sql.DB, cfg config.Config) error {
	if cfg.SuperuserName == "" {
		return nil
	}

	marker := "?"
	if cfg.DBEngine == "postgres" {
		marker = "$1"
	}
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM auth_user WHERE username = `+marker+`)`, cfg.SuperuserName).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	repository := auth.NewRepository(db, cfg.DBEngine)
	user := auth.NewUser{
		Username:    cfg.SuperuserName,
		Password:    cfg.SuperuserPassword,
		IsStaff:     true,
		IsSuperuser: true,
	}
	if cfg.SuperuserPassword == "" {
		_, err = repository.CreateUnusableUser(ctx, user)
	} else {
		_, err = repository.CreateUser(ctx, user)
	}
	return err
}
