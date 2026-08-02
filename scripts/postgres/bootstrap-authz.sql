-- Bootstrap test logins before the server migration runs.
-- Role creation is idempotent because the migration also owns the NOLOGIN roles.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_admin') THEN
        CREATE ROLE cortex_admin NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_test') THEN
        CREATE ROLE cortex_test LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'cortex_test';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'cortex_admin_login') THEN
        CREATE ROLE cortex_admin_login LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'cortex_admin_login';
    END IF;
END
$$;

GRANT cortex_admin TO cortex_admin_login;
