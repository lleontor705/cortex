#!/bin/bash
# Role bootstrap for the production compose stack (docker-compose.prod.yml).
# Runs once per fresh PostgreSQL data directory via /docker-entrypoint-initdb.d/.
#
# It provisions the two-role production model the server composition enforces
# (resolveServerDSNs fails closed when runtime and migration DSNs share a role):
#   - <app user>       LOGIN NOBYPASSRLS  — runtime request plane, RLS-restricted
#   - <migration user> LOGIN BYPASSRLS    — privileged migrations + bootstrap reconciler
#   - cortex_admin     NOLOGIN            — group role granted limited EXECUTE/SELECT
# The migration role also becomes the database owner so the embedded server
# migrations run cleanly (CREATE EXTENSION pgcrypto is a trusted extension;
# schema/DDL ownership; ALTER ... OWNER TO cortex_migration).
set -euo pipefail

: "${POSTGRES_APP_USER:=cortex_app}"
: "${POSTGRES_APP_PASSWORD:=cortex_app_local}"
: "${POSTGRES_MIGRATION_USER:=cortex_migration}"
: "${POSTGRES_MIGRATION_PASSWORD:=cortex_migration_local}"

if [ "$POSTGRES_APP_USER" = "$POSTGRES_MIGRATION_USER" ]; then
  echo "cortex-init: POSTGRES_APP_USER and POSTGRES_MIGRATION_USER must be distinct PostgreSQL roles" >&2
  exit 1
fi

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
CREATE ROLE "$POSTGRES_APP_USER" LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD '$POSTGRES_APP_PASSWORD';
CREATE ROLE "$POSTGRES_MIGRATION_USER" LOGIN NOSUPERUSER BYPASSRLS PASSWORD '$POSTGRES_MIGRATION_PASSWORD';
CREATE ROLE cortex_admin NOLOGIN;
ALTER DATABASE "$POSTGRES_DB" OWNER TO "$POSTGRES_MIGRATION_USER";
EOSQL

echo "cortex-init: provisioned runtime role '$POSTGRES_APP_USER' (NOBYPASSRLS) and migration role '$POSTGRES_MIGRATION_USER' (BYPASSRLS)"
