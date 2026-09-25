-- +goose Up
CREATE TABLE linkding_job (
    id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    kind text NOT NULL,
    payload text NOT NULL CHECK (json_valid(payload)),
    status text NOT NULL CHECK (status IN ('pending', 'running', 'complete', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    lease_token text NOT NULL DEFAULT '',
    available_at datetime NOT NULL,
    leased_until datetime NULL,
    last_error text NOT NULL DEFAULT '',
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL
);
CREATE INDEX linkding_job_ready_idx ON linkding_job (status, available_at, id);
CREATE INDEX linkding_job_lease_idx ON linkding_job (status, leased_until);
