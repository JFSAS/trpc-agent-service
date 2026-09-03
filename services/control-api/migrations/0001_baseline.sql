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

CREATE TABLE agents (
    tenant_id            text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    id                   text NOT NULL,
    name                 text NOT NULL,
    description          text NOT NULL DEFAULT '',
    latest_version_number bigint,
    created_by           text NOT NULL REFERENCES user_accounts(id),
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT agents_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT agents_latest_version_positive
        CHECK (latest_version_number IS NULL OR latest_version_number > 0)
);

CREATE INDEX agents_tenant_updated_idx
    ON agents (tenant_id, updated_at DESC, id);

CREATE TABLE agent_drafts (
    tenant_id    text NOT NULL,
    agent_id     text NOT NULL,
    spec_revision bigint NOT NULL,
    spec_jsonb   jsonb NOT NULL,
    updated_by   text NOT NULL REFERENCES user_accounts(id),
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, agent_id),
    CONSTRAINT agent_drafts_agent_fk
        FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agents(tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT agent_drafts_revision_positive CHECK (spec_revision > 0),
    CONSTRAINT agent_drafts_spec_object CHECK (jsonb_typeof(spec_jsonb) = 'object')
);

CREATE TABLE agent_versions (
    tenant_id             text NOT NULL,
    id                    text NOT NULL,
    agent_id              text NOT NULL,
    version_number        bigint NOT NULL,
    source_draft_revision bigint NOT NULL,
    schema_version        text NOT NULL,
    spec_jsonb            jsonb NOT NULL,
    spec_digest           text NOT NULL,
    published_by          text NOT NULL REFERENCES user_accounts(id),
    published_at          timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT agent_versions_agent_fk
        FOREIGN KEY (tenant_id, agent_id)
        REFERENCES agents(tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT agent_versions_number_positive CHECK (version_number > 0),
    CONSTRAINT agent_versions_source_revision_positive
        CHECK (source_draft_revision > 0),
    CONSTRAINT agent_versions_spec_object CHECK (jsonb_typeof(spec_jsonb) = 'object'),
    CONSTRAINT agent_versions_digest_format
        CHECK (spec_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT agent_versions_tenant_agent_number_unique
        UNIQUE (tenant_id, agent_id, version_number),
    CONSTRAINT agent_versions_tenant_agent_source_revision_unique
        UNIQUE (tenant_id, agent_id, source_draft_revision)
);

CREATE INDEX agent_versions_tenant_agent_published_idx
    ON agent_versions (tenant_id, agent_id, published_at DESC);

CREATE FUNCTION reject_agent_version_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'agent_versions are immutable';
END
$$;

CREATE TRIGGER agent_versions_immutable
BEFORE UPDATE OR DELETE ON agent_versions
FOR EACH ROW EXECUTE FUNCTION reject_agent_version_mutation();

CREATE TABLE runtime_profiles (
    tenant_id             text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    id                    text NOT NULL,
    name                  text NOT NULL,
    description           text NOT NULL DEFAULT '',
    latest_revision_number bigint,
    created_by            text NOT NULL REFERENCES user_accounts(id),
    created_at            timestamptz NOT NULL,
    updated_at            timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT runtime_profiles_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT runtime_profiles_latest_revision_positive
        CHECK (latest_revision_number IS NULL OR latest_revision_number > 0)
);

CREATE INDEX runtime_profiles_tenant_updated_idx
    ON runtime_profiles (tenant_id, updated_at DESC, id);

CREATE TABLE runtime_profile_drafts (
    tenant_id     text NOT NULL,
    profile_id    text NOT NULL,
    spec_revision bigint NOT NULL,
    spec_jsonb    jsonb NOT NULL,
    updated_by    text NOT NULL REFERENCES user_accounts(id),
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, profile_id),
    CONSTRAINT runtime_profile_drafts_profile_fk
        FOREIGN KEY (tenant_id, profile_id)
        REFERENCES runtime_profiles(tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT runtime_profile_drafts_revision_positive CHECK (spec_revision > 0),
    CONSTRAINT runtime_profile_drafts_spec_object CHECK (jsonb_typeof(spec_jsonb) = 'object')
);

CREATE TABLE runtime_profile_revisions (
    tenant_id             text NOT NULL,
    id                    text NOT NULL,
    profile_id            text NOT NULL,
    revision_number       bigint NOT NULL,
    source_draft_revision bigint NOT NULL,
    schema_version        text NOT NULL,
    spec_jsonb            jsonb NOT NULL,
    spec_digest           text NOT NULL,
    published_by          text NOT NULL REFERENCES user_accounts(id),
    published_at          timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT runtime_profile_revisions_profile_fk
        FOREIGN KEY (tenant_id, profile_id)
        REFERENCES runtime_profiles(tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT runtime_profile_revisions_number_positive CHECK (revision_number > 0),
    CONSTRAINT runtime_profile_revisions_source_revision_positive
        CHECK (source_draft_revision > 0),
    CONSTRAINT runtime_profile_revisions_spec_object
        CHECK (jsonb_typeof(spec_jsonb) = 'object'),
    CONSTRAINT runtime_profile_revisions_digest_format
        CHECK (spec_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT runtime_profile_revisions_tenant_profile_number_unique
        UNIQUE (tenant_id, profile_id, revision_number),
    CONSTRAINT runtime_profile_revisions_tenant_profile_source_unique
        UNIQUE (tenant_id, profile_id, source_draft_revision)
);

CREATE INDEX runtime_profile_revisions_tenant_profile_published_idx
    ON runtime_profile_revisions (tenant_id, profile_id, published_at DESC);

CREATE FUNCTION reject_runtime_profile_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'runtime_profile_revisions are immutable';
END
$$;

CREATE TRIGGER runtime_profile_revisions_immutable
BEFORE UPDATE OR DELETE ON runtime_profile_revisions
FOR EACH ROW EXECUTE FUNCTION reject_runtime_profile_revision_mutation();
