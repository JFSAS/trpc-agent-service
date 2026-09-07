# Control API migrations

Control API owns and embeds the migrations in this directory. Startup records
each applied file in `control_schema_migrations` while holding a PostgreSQL
transaction-level advisory lock. Ledger creation itself occurs after that lock
inside the migration transaction, so concurrent first startups cannot race on
creating the ledger. Production bootstrap uses the explicit
`CONTROL_MIGRATION_DATABASE_URL`, checks it against the independent runtime
connection's actual database/schema, and closes the migration pool before handing
the runtime pool to modules. Production startup fixes the names to the `control`
schema and `control_migrator`/`control_runtime` roles. It rejects global admin
attributes on either role; the migrator must own the schema while the runtime
must not own or create in it. Another workload's matching DSNs are rejected before
any DDL.

The schema is selected by the trusted connection/role `search_path`; historical
SQL remains unqualified and unchanged. `MigrateForRuntime` revokes the runtime
role's mutation privileges on the ledger inside the same transaction that creates
it, retaining only `SELECT`. `Migrate` remains available for existing dedicated
integration fixtures; it is not the production startup path. Provisioning must
preserve ledger grants and the runtime role must not inherit the schema owner.

`0001_baseline.sql` contains the V1 Identity, Platform Operator, Tenant, Membership,
Agent, Agent Draft, immutable Agent Version, Runtime Profile, Profile Draft,
immutable Profile Revision, private Profile Credential, Deployment, immutable
Deployment Revision and Runtime Manifest, command Receipt, and Control Outbox
schema. The baseline may grow while the greenfield V1 schema is still under
construction; after the first released deployment, every schema change must use
a new migration.

Changing this greenfield baseline does not replay it in a database where
`0001_baseline.sql` is already present in `control_schema_migrations`. Development
and test databases created from an older baseline must be rebuilt instead of
receiving a compatibility migration before the first release.

`0002_channel_preflights.sql` is an additive upgrade for Telegram read-only
diagnostics from the currently deployed Channel baseline. It adds only
`channel_preflights` and `channel_preflight_requests` plus their indexes. The
existing baseline is not rewritten; existing accounts, credentials, routes,
observations, command receipts, and Outbox records remain unchanged. Existing
databases apply this file once through the normal embedded migration runner.
The diagnostic wire protocol remains V1; a numbered SQL migration is not a
new product/API version. Rollback testing uses an isolated database/schema copy,
not table removal from an existing deployment.
