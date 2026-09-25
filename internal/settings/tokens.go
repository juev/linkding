package settings

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type APIToken struct {
	ID      int64
	Name    string
	Created time.Time
}

func ListAPITokens(ctx context.Context, db *sql.DB, engine string, userID int64) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, created FROM bookmarks_apitoken WHERE user_id = `+marker(engine, 1)+` ORDER BY created DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []APIToken
	for rows.Next() {
		var token APIToken
		if err := rows.Scan(&token.ID, &token.Name, &token.Created); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func CreateAPIToken(ctx context.Context, db *sql.DB, engine string, userID int64, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "API Token"
	}
	if len([]rune(name)) > 128 {
		return "", "", ValidationError{Field: "name"}
	}
	key, err := randomTokenKey()
	if err != nil {
		return "", "", err
	}
	query := `INSERT INTO bookmarks_apitoken (key, name, created, user_id) VALUES (` + marker(engine, 1) + `,` + marker(engine, 2) + `,` + marker(engine, 3) + `,` + marker(engine, 4) + `)`
	if _, err := db.ExecContext(ctx, query, key, name, time.Now().UTC(), userID); err != nil {
		return "", "", err
	}
	return key, name, nil
}

func DeleteAPIToken(ctx context.Context, db *sql.DB, engine string, userID, tokenID int64) (string, error) {
	var name string
	query := `SELECT name FROM bookmarks_apitoken WHERE id = ` + marker(engine, 1) + ` AND user_id = ` + marker(engine, 2)
	if err := db.QueryRowContext(ctx, query, tokenID, userID).Scan(&name); err != nil {
		return "", err
	}
	query = `DELETE FROM bookmarks_apitoken WHERE id = ` + marker(engine, 1) + ` AND user_id = ` + marker(engine, 2)
	result, err := db.ExecContext(ctx, query, tokenID, userID)
	if err != nil {
		return "", err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if rows != 1 {
		return "", sql.ErrNoRows
	}
	return name, nil
}

func EnsureFeedToken(ctx context.Context, db *sql.DB, engine string, userID int64) (string, error) {
	query := `SELECT key FROM bookmarks_feedtoken WHERE user_id = ` + marker(engine, 1)
	var key string
	err := db.QueryRowContext(ctx, query, userID).Scan(&key)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	key, err = randomTokenKey()
	if err != nil {
		return "", err
	}
	query = `INSERT INTO bookmarks_feedtoken (key, created, user_id) VALUES (` + marker(engine, 1) + `,` + marker(engine, 2) + `,` + marker(engine, 3) + `)`
	if _, err := db.ExecContext(ctx, query, key, time.Now().UTC(), userID); err != nil {
		// A concurrent page load may have created the user's one-to-one token.
		lookupErr := db.QueryRowContext(ctx, `SELECT key FROM bookmarks_feedtoken WHERE user_id = `+marker(engine, 1), userID).Scan(&key)
		if lookupErr == nil {
			return key, nil
		}
		return "", fmt.Errorf("create feed token: %w", err)
	}
	return key, nil
}

func randomTokenKey() (string, error) {
	var value [20]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
