CREATE TABLE user_accounts (
    id                  text PRIMARY KEY,
    username            text NOT NULL,
    normalized_username text NOT NULL,
    display_name        text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'ACTIVE'
                        CHECK (status IN ('ACTIVE', 'DISABLED')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_accounts_normalized_username_unique
        UNIQUE (normalized_username)
);

CREATE TABLE password_credentials (
    user_id                   text PRIMARY KEY
                              REFERENCES user_accounts(id) ON DELETE CASCADE,
    encoded_hash              text NOT NULL,
    must_change_at_next_login boolean NOT NULL DEFAULT false,
    changed_at                timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_sessions (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    restricted boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT user_sessions_token_hash_unique UNIQUE (token_hash),
    CONSTRAINT user_sessions_expiry_after_creation
        CHECK (expires_at > created_at)
);

CREATE INDEX user_sessions_active_user_idx
    ON user_sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE platform_operator_grants (
    user_id               text PRIMARY KEY
                           REFERENCES user_accounts(id) ON DELETE CASCADE,
    granted_by_actor_type text NOT NULL
                           CHECK (granted_by_actor_type IN ('USER', 'SYSTEM_BOOTSTRAP')),
    granted_by_user_id    text REFERENCES user_accounts(id) ON DELETE SET NULL,
    granted_at            timestamptz NOT NULL,
    revoked_by_user_id    text REFERENCES user_accounts(id) ON DELETE SET NULL,
    revoked_at            timestamptz,
    CONSTRAINT platform_operator_grant_actor_valid CHECK (
        (granted_by_actor_type = 'SYSTEM_BOOTSTRAP' AND granted_by_user_id IS NULL)
        OR
        (granted_by_actor_type = 'USER' AND granted_by_user_id IS NOT NULL)
    )
);

CREATE INDEX platform_operator_grants_active_idx
    ON platform_operator_grants (granted_at, user_id)
    WHERE revoked_at IS NULL;

CREATE TABLE tenants (
    id         text PRIMARY KEY,
    slug       text NOT NULL,
    name       text NOT NULL,
    status     text NOT NULL DEFAULT 'ACTIVE'
               CHECK (status IN ('ACTIVE')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT tenants_slug_unique UNIQUE (slug)
);

CREATE TABLE tenant_memberships (
    id         text PRIMARY KEY,
    tenant_id  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('OWNER', 'MEMBER')),
    created_by text NOT NULL REFERENCES user_accounts(id),
    created_at timestamptz NOT NULL,
    CONSTRAINT tenant_memberships_tenant_user_unique UNIQUE (tenant_id, user_id)
);

CREATE INDEX tenant_memberships_user_idx
    ON tenant_memberships (user_id, tenant_id);
