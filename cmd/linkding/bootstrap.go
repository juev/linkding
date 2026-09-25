package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/juev/linkding/internal/auth"
	"github.com/juev/linkding/internal/config"
)

func ensureInitialSuperuser(ctx context.Context, db *sql.DB, cfg config.Config) error {
	if cfg.SuperuserName == "" {
		return nil
	}
	return ensureSuperuser(ctx, db, cfg.DBEngine, cfg.SuperuserName, "", cfg.SuperuserPassword)
}

func ensureSuperuser(ctx context.Context, db *sql.DB, engine, username, email, password string) error {
	if username == "" {
		return fmt.Errorf("superuser username is required")
	}
	marker := "?"
	if engine == "postgres" {
		marker = "$1"
	}
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM auth_user WHERE username = `+marker+`)`, username).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	repository := auth.NewRepository(db, engine)
	user := auth.NewUser{
		Username:    username,
		Email:       email,
		Password:    password,
		IsStaff:     true,
		IsSuperuser: true,
	}
	if password == "" {
		_, err = repository.CreateUnusableUser(ctx, user)
	} else {
		_, err = repository.CreateUser(ctx, user)
	}
	return err
}
