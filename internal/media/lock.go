package media

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrSnapshotBusy = errors.New("snapshot processor is busy")

func (p *Processor) acquireSnapshotLock(ctx context.Context) (func() error, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random[:])
	now := time.Now().UTC()
	lease := 10 * time.Minute
	if p.config.SingleFileTimeoutSec > 0 {
		timeout := time.Duration(p.config.SingleFileTimeoutSec*float64(time.Second)) + time.Minute
		if timeout > lease {
			lease = timeout
		}
	}
	query := `INSERT INTO linkding_lock (name, token, leased_until) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET token = excluded.token, leased_until = excluded.leased_until
		WHERE linkding_lock.leased_until <= ? RETURNING token`
	if p.engine == "postgres" {
		query = `INSERT INTO linkding_lock (name, token, leased_until) VALUES ($1, $2, $3)
			ON CONFLICT (name) DO UPDATE SET token = excluded.token, leased_until = excluded.leased_until
			WHERE linkding_lock.leased_until <= $4 RETURNING token`
	}
	var claimed string
	err := p.db.QueryRowContext(ctx, query, "html-snapshots-lock", token, now.Add(lease), now).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSnapshotBusy
	}
	if err != nil {
		return nil, fmt.Errorf("acquire snapshot lock: %w", err)
	}
	if claimed != token {
		return nil, ErrSnapshotBusy
	}
	return func() error {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		query := "DELETE FROM linkding_lock WHERE name = " + p.marker(1) + " AND token = " + p.marker(2)
		_, err := p.db.ExecContext(releaseCtx, query, "html-snapshots-lock", token)
		return err
	}, nil
}
