#!/bin/sh
set -eu
: "${GATEWAY_POSTGRES_PASSWORD:?Gateway database password required}"
# Fixed role/database names; SQL values use psql quoting, not shell SQL interpolation.
psql --no-psqlrc --set=ON_ERROR_STOP=1 --set=gateway_password="$GATEWAY_POSTGRES_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE gateway LOGIN PASSWORD %L', :'gateway_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='gateway') \gexec
SELECT format('ALTER ROLE gateway LOGIN PASSWORD %L', :'gateway_password') \gexec
SELECT 'CREATE DATABASE channel_gateway OWNER gateway'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='channel_gateway') \gexec
REVOKE ALL ON DATABASE channel_gateway FROM PUBLIC;
GRANT CONNECT ON DATABASE channel_gateway TO gateway;
SQL
PGDATABASE=channel_gateway psql --no-psqlrc --set=ON_ERROR_STOP=1 <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO gateway;
SQL
