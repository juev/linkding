package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrLeaseLost = errors.New("job lease lost")

type Job struct {
	ID       int64
	Kind     string
	Payload  json.RawMessage
	Attempts int
	// MaxAttempts is set by the worker before dispatch; it is not stored in the queue.
	MaxAttempts int
	LeaseToken  string
}

type Queue struct {
	db     *sql.DB
	engine string
}

func New(db *sql.DB, engine string) *Queue { return &Queue{db: db, engine: engine} }

// EnqueueTx allows a job and the record that requires it to commit together.
func (q *Queue) EnqueueTx(ctx context.Context, tx *sql.Tx, kind string, payload json.RawMessage) (int64, error) {
	if kind == "" || !json.Valid(payload) {
		return 0, fmt.Errorf("job kind and JSON payload are required")
	}
	now := time.Now().UTC()
	query := `INSERT INTO linkding_job (kind, payload, status, available_at, created_at, updated_at)
		VALUES (?, ?, 'pending', ?, ?, ?) RETURNING id`
	if q.engine == "postgres" {
		query = `INSERT INTO linkding_job (kind, payload, status, available_at, created_at, updated_at)
			VALUES ($1, $2, 'pending', $3, $4, $5) RETURNING id`
	}
	var id int64
	if err := tx.QueryRowContext(ctx, query, kind, string(payload), now, now, now).Scan(&id); err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return id, nil
}

func (q *Queue) Enqueue(ctx context.Context, kind string, payload json.RawMessage) (int64, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := q.EnqueueTx(ctx, tx, kind, payload)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Claim atomically takes one ready job. An expired lease is eligible again,
// so handlers must make externally visible effects idempotent.
func (q *Queue) Claim(ctx context.Context, lease time.Duration) (*Job, error) {
	return q.ClaimByKind(ctx, lease, "", false)
}

// ClaimByKind scopes a worker lane to one kind, or excludes that kind. An
// empty kind keeps the original all-jobs claim behavior.
func (q *Queue) ClaimByKind(ctx context.Context, lease time.Duration, kind string, exclude bool) (*Job, error) {
	if lease <= 0 {
		return nil, fmt.Errorf("job lease must be positive")
	}
	if exclude && kind == "" {
		return nil, fmt.Errorf("excluded job kind is required")
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return nil, fmt.Errorf("job lease token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes[:])
	now := time.Now().UTC()
	until := now.Add(lease)
	var row *sql.Row
	if q.engine == "postgres" {
		kindClause := ""
		args := []any{now, token, until}
		if kind != "" {
			comparison := "="
			if exclude {
				comparison = "<>"
			}
			kindClause = " AND kind " + comparison + " $4"
			args = append(args, kind)
		}
		row = q.db.QueryRowContext(ctx, `WITH selected AS (
			SELECT id FROM linkding_job
			WHERE ((status = 'pending' AND available_at <= $1)
			   OR (status = 'running' AND leased_until <= $1))`+kindClause+`
			ORDER BY available_at, id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE linkding_job AS j SET status = 'running', attempts = j.attempts + 1,
			lease_token = $2, leased_until = $3, updated_at = $1
		FROM selected WHERE j.id = selected.id
		RETURNING j.id, j.kind, j.payload, j.attempts, j.lease_token`, args...)
	} else {
		kindClause := ""
		args := []any{token, until, now, now, now}
		if kind != "" {
			comparison := "="
			if exclude {
				comparison = "<>"
			}
			kindClause = " AND kind " + comparison + " ?"
			args = append(args, kind)
		}
		row = q.db.QueryRowContext(ctx, `UPDATE linkding_job
			SET status = 'running', attempts = attempts + 1,
				lease_token = ?, leased_until = ?, updated_at = ?
			WHERE id = (SELECT id FROM linkding_job
				WHERE ((status = 'pending' AND available_at <= ?)
				   OR (status = 'running' AND leased_until <= ?))`+kindClause+`
				ORDER BY available_at, id LIMIT 1)
			RETURNING id, kind, payload, attempts, lease_token`, args...)
	}
	var job Job
	var payload string
	if err := row.Scan(&job.ID, &job.Kind, &payload, &job.Attempts, &job.LeaseToken); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim job: %w", err)
	}
	job.Payload = json.RawMessage(payload)
	return &job, nil
}

func (q *Queue) Complete(ctx context.Context, job Job) error {
	query := `UPDATE linkding_job SET status = 'complete', lease_token = '', leased_until = NULL,
		updated_at = ? WHERE id = ? AND status = 'running' AND lease_token = ?`
	if q.engine == "postgres" {
		query = `UPDATE linkding_job SET status = 'complete', lease_token = '', leased_until = NULL,
			updated_at = $1 WHERE id = $2 AND status = 'running' AND lease_token = $3`
	}
	result, err := q.db.ExecContext(ctx, query, time.Now().UTC(), job.ID, job.LeaseToken)
	return checkLease(result, err)
}

// Renew extends a live lease while a long-running handler is still active.
func (q *Queue) Renew(ctx context.Context, job Job, lease time.Duration) error {
	if lease <= 0 {
		return fmt.Errorf("job lease must be positive")
	}
	now := time.Now().UTC()
	query := `UPDATE linkding_job SET leased_until = ?, updated_at = ?
		WHERE id = ? AND status = 'running' AND lease_token = ?`
	if q.engine == "postgres" {
		query = `UPDATE linkding_job SET leased_until = $1, updated_at = $2
			WHERE id = $3 AND status = 'running' AND lease_token = $4`
	}
	result, err := q.db.ExecContext(ctx, query, now.Add(lease), now, job.ID, job.LeaseToken)
	return checkLease(result, err)
}

// Fail retries until maxAttempts has been reached; maxAttempts counts claims.
func (q *Queue) Fail(ctx context.Context, job Job, reason string, maxAttempts int, delay time.Duration) error {
	if maxAttempts < 1 || delay < 0 {
		return fmt.Errorf("invalid job retry policy")
	}
	status := "pending"
	if job.Attempts >= maxAttempts {
		status = "failed"
	}
	now := time.Now().UTC()
	query := `UPDATE linkding_job SET status = ?, lease_token = '', leased_until = NULL,
		last_error = ?, available_at = ?, updated_at = ?
		WHERE id = ? AND status = 'running' AND lease_token = ?`
	if q.engine == "postgres" {
		query = `UPDATE linkding_job SET status = $1, lease_token = '', leased_until = NULL,
			last_error = $2, available_at = $3, updated_at = $4
			WHERE id = $5 AND status = 'running' AND lease_token = $6`
	}
	result, err := q.db.ExecContext(ctx, query, status, reason, now.Add(delay), now, job.ID, job.LeaseToken)
	return checkLease(result, err)
}

func checkLease(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseLost
	}
	return nil
}
