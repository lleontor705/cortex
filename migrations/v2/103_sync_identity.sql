-- Canonicalize server-native rows and backfill the cursor-zero change feed.
CREATE OR REPLACE FUNCTION cortex_default_sync_client_id() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.client_id := COALESCE(NEW.client_id, NEW.public_id::text);
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS sessions_default_sync_client_id ON sessions;
CREATE TRIGGER sessions_default_sync_client_id BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION cortex_default_sync_client_id();
DROP TRIGGER IF EXISTS observations_default_sync_client_id ON observations;
CREATE TRIGGER observations_default_sync_client_id BEFORE INSERT ON observations FOR EACH ROW EXECUTE FUNCTION cortex_default_sync_client_id();
DROP TRIGGER IF EXISTS prompts_default_sync_client_id ON prompts;
CREATE TRIGGER prompts_default_sync_client_id BEFORE INSERT ON prompts FOR EACH ROW EXECUTE FUNCTION cortex_default_sync_client_id();
DROP TRIGGER IF EXISTS edges_default_sync_client_id ON edges;
CREATE TRIGGER edges_default_sync_client_id BEFORE INSERT ON edges FOR EACH ROW EXECUTE FUNCTION cortex_default_sync_client_id();
