# Control API migrations

Control API owns and embeds the migrations in this directory. Startup records
each applied file in `control_schema_migrations` while holding a PostgreSQL
transaction-level advisory lock.

`0001_baseline.sql` contains the V1 Identity, Platform Operator, Tenant, Membership,
Agent, Agent Draft, immutable Agent Version, Runtime Profile, Profile Draft, and
immutable Profile Revision schema. The baseline may grow while the greenfield V1
schema is still under construction; after the first released deployment, every
schema change must use a new migration.
