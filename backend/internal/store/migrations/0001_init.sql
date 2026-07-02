-- +goose Up

CREATE TABLE users (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    phone            text NOT NULL UNIQUE,
    display_name     text NOT NULL DEFAULT '',
    is_admin         boolean NOT NULL DEFAULT false,
    trust_tier       integer NOT NULL DEFAULT 0,
    strikes          integer NOT NULL DEFAULT 0,
    bank_account_ref text,
    bvn_ref          text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pockets (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    short_code                text NOT NULL UNIQUE,
    structure                 text NOT NULL CHECK (structure IN ('p2p', 'brokered')),
    creator_role              text NOT NULL CHECK (creator_role IN ('buyer', 'vendor', 'broker')),
    amount_kobo               bigint NOT NULL CHECK (amount_kobo > 0),
    commission_kobo           bigint NOT NULL DEFAULT 0 CHECK (commission_kobo >= 0),
    premium_kobo              bigint NOT NULL DEFAULT 0 CHECK (premium_kobo >= 0),
    buyer_total_kobo          bigint NOT NULL CHECK (buyer_total_kobo = amount_kobo + commission_kobo + premium_kobo),
    item_description          text NOT NULL,
    category                  text NOT NULL DEFAULT 'general',
    delivery_address          text,
    delivery_deadline         timestamptz,
    mode                      text NOT NULL CHECK (mode IN ('instant', 'cooldown')),
    inspection_window_minutes integer NOT NULL DEFAULT 0 CHECK (inspection_window_minutes >= 0),
    state                     text NOT NULL,
    release_code_hash         text,
    code_attempts             integer NOT NULL DEFAULT 0,
    code_locked               boolean NOT NULL DEFAULT false,
    settle_after              timestamptz,
    grace_deadline            timestamptz,
    funding_expires_at        timestamptz,
    version                   integer NOT NULL DEFAULT 1,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT brokered_has_commission CHECK (structure = 'brokered' OR commission_kobo = 0)
);

CREATE INDEX pockets_state_idx ON pockets (state);

CREATE TABLE pocket_participants (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pocket_id       uuid NOT NULL REFERENCES pockets (id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('buyer', 'vendor', 'broker')),
    user_id         uuid REFERENCES users (id),
    link_token_hash text NOT NULL,
    accepted_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pocket_id, role)
);

CREATE TABLE pocket_events (
    id         bigserial PRIMARY KEY,
    pocket_id  uuid NOT NULL REFERENCES pockets (id) ON DELETE CASCADE,
    actor      text NOT NULL CHECK (actor IN ('buyer', 'vendor', 'broker', 'admin', 'system')),
    from_state text,
    to_state   text,
    kind       text NOT NULL,
    payload    jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pocket_events_pocket_idx ON pocket_events (pocket_id);

CREATE TABLE disputes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pocket_id   uuid NOT NULL UNIQUE REFERENCES pockets (id) ON DELETE CASCADE,
    class       text NOT NULL CHECK (class IN ('not_delivered', 'not_as_described')),
    opened_by   text NOT NULL CHECK (opened_by IN ('buyer', 'vendor', 'system')),
    state       text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'resolved')),
    resolution  text CHECK (resolution IN ('refund', 'payout')),
    resolved_by uuid REFERENCES users (id),
    notes       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE evidence (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pocket_id       uuid NOT NULL REFERENCES pockets (id) ON DELETE CASCADE,
    party           text NOT NULL CHECK (party IN ('buyer', 'vendor', 'broker')),
    type            text NOT NULL CHECK (type IN ('unboxing_video', 'dispatch_proof', 'packing_media', 'photo')),
    storage_ref     text NOT NULL,
    captured_in_app boolean NOT NULL DEFAULT false,
    captured_at     timestamptz NOT NULL,
    within_window   boolean,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX evidence_pocket_idx ON evidence (pocket_id);

CREATE TABLE settlements (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pocket_id           uuid NOT NULL REFERENCES pockets (id) ON DELETE CASCADE,
    direction           text NOT NULL CHECK (direction IN ('payout', 'refund')),
    beneficiary_role    text NOT NULL CHECK (beneficiary_role IN ('vendor', 'broker', 'buyer')),
    beneficiary_user_id uuid REFERENCES users (id),
    amount_kobo         bigint NOT NULL CHECK (amount_kobo > 0),
    idempotency_key     text NOT NULL UNIQUE,
    gateway_ref         text,
    status              text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pocket_id, beneficiary_role, direction)
);

CREATE INDEX settlements_pocket_idx ON settlements (pocket_id);

CREATE TABLE webhook_events (
    provider_event_id text PRIMARY KEY,
    payload           jsonb NOT NULL,
    processed_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down

DROP TABLE webhook_events;
DROP TABLE settlements;
DROP TABLE evidence;
DROP TABLE disputes;
DROP TABLE pocket_events;
DROP TABLE pocket_participants;
DROP TABLE pockets;
DROP TABLE users;
