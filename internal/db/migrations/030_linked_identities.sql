-- +goose Up
-- Separate "login identities" (user_identities, N:1, UNIQUE issuer+subject)
-- from "authorization identities" (linked_identities, N:M, UNIQUE per user).
-- Multiple SWAMP users may link to the same external account (e.g. GitHub).

CREATE TABLE linked_identities (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer            TEXT NOT NULL,
    subject           TEXT NOT NULL,
    email             TEXT NOT NULL DEFAULT '',
    display_name      TEXT NOT NULL DEFAULT '',
    idp_name          TEXT NOT NULL DEFAULT '',
    access_token_enc  TEXT,
    refresh_token_enc TEXT,
    token_expires_at  TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, issuer, subject)
);

CREATE INDEX idx_linked_identities_user ON linked_identities(user_id);

-- Migrate existing GitHub identity rows from user_identities.
INSERT INTO linked_identities
    (id, user_id, issuer, subject, email, display_name, idp_name,
     access_token_enc, refresh_token_enc, token_expires_at, created_at, updated_at)
SELECT id, user_id, issuer, subject, email, display_name, idp_name,
       access_token_enc, refresh_token_enc, token_expires_at, created_at,
       COALESCE(updated_at, created_at)
FROM user_identities
WHERE issuer = 'https://github.com'
ON CONFLICT (user_id, issuer, subject) DO NOTHING;

DELETE FROM user_identities WHERE issuer = 'https://github.com';

-- +goose Down
-- Move GitHub rows back to user_identities (tokens may be stale after rollback).
INSERT INTO user_identities
    (id, user_id, issuer, subject, email, display_name, idp_name,
     access_token_enc, refresh_token_enc, token_expires_at, created_at, updated_at)
SELECT id, user_id, issuer, subject, email, display_name, idp_name,
       access_token_enc, refresh_token_enc, token_expires_at, created_at, updated_at
FROM linked_identities
WHERE issuer = 'https://github.com'
ON CONFLICT (issuer, subject) DO NOTHING;

DROP TABLE IF EXISTS linked_identities;
