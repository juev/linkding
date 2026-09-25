-- +goose Up
CREATE TABLE linkding_lock (
    name text NOT NULL PRIMARY KEY,
    token text NOT NULL,
    leased_until timestamp with time zone NOT NULL
);
