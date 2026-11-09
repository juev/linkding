-- +goose Up
CREATE TABLE linkding_passkey (
    id integer NOT NULL PRIMARY KEY AUTOINCREMENT,
    user_id integer NOT NULL REFERENCES auth_user (id) ON DELETE CASCADE,
    credential_id text NOT NULL UNIQUE,
    name varchar(128) NOT NULL,
    credential text NOT NULL,
    created_at datetime NOT NULL,
    last_used_at datetime NULL
);
CREATE INDEX linkding_passkey_user_idx ON linkding_passkey (user_id);

CREATE TABLE linkding_webauthn_challenge (
    challenge_key text NOT NULL PRIMARY KEY,
    kind text NOT NULL,
    user_id integer NULL REFERENCES auth_user (id) ON DELETE CASCADE,
    session_data text NOT NULL,
    expires_at datetime NOT NULL
);

CREATE TABLE linkding_recovery_token (
    token_hash text NOT NULL PRIMARY KEY,
    user_id integer NOT NULL REFERENCES auth_user (id) ON DELETE CASCADE,
    created_at datetime NOT NULL,
    expires_at datetime NOT NULL
);

-- +goose Down
DROP TABLE linkding_recovery_token;
DROP TABLE linkding_webauthn_challenge;
DROP TABLE linkding_passkey;
