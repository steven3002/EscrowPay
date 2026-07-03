-- +goose Up

-- Account identity. A user row is created either by a verified external
-- identity (Google OIDC subject) or, in sandbox mode, by a demo login keyed on
-- phone. Phone therefore becomes optional: an OIDC-created account has no phone
-- until the user supplies one.
ALTER TABLE users ALTER COLUMN phone DROP NOT NULL;
ALTER TABLE users ADD COLUMN email text UNIQUE;
ALTER TABLE users ADD COLUMN google_sub text UNIQUE;
ALTER TABLE users ADD COLUMN avatar_url text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN last_login_at timestamptz;

-- Server-side sessions. The cookie carries a random bearer token; only its
-- SHA-256 is stored, so a database-only compromise cannot mint a valid cookie.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT '',
    revoked_at   timestamptz
);

CREATE INDEX sessions_user_idx ON sessions (user_id);

-- A user holds at most one role per pocket: the escrow's guarantees assume the
-- buyer, vendor and broker are distinct parties.
CREATE UNIQUE INDEX pocket_participants_user_unique ON pocket_participants (pocket_id, user_id) WHERE user_id IS NOT NULL;

-- +goose Down

DROP INDEX pocket_participants_user_unique;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE users DROP COLUMN avatar_url;
ALTER TABLE users DROP COLUMN google_sub;
ALTER TABLE users DROP COLUMN email;
ALTER TABLE users ALTER COLUMN phone SET NOT NULL;
