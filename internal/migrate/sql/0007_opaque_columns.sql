-- Two columns were typed JSON and then stopped holding JSON.
--
-- email_providers.config holds the provider configuration *encrypted*
-- (enc:v2:<key>:<base64>). Encryption was added after the column type was
-- chosen and the type was never revisited, so on PostgreSQL — where JSONB is
-- validated — creating any provider fails outright with "invalid input syntax
-- for type json". SQLite renders JSON as TEXT and validates nothing, which is
-- why this went unnoticed for as long as SQLite was the only engine anyone ran.
--
-- webhook_deliveries.payload holds the notification body as it was built. JSONB
-- is not a store, it is a parse: it reorders keys and rewrites whitespace, so
-- what comes back out is equivalent JSON but different bytes. That column
-- exists precisely to preserve what was promised at the moment the event
-- happened, and normalising it defeats the point.
--
-- Neither column is ever queried by its JSON structure, so nothing is lost.
{{PG_ONLY}} ALTER TABLE email_providers ALTER COLUMN config TYPE TEXT;
{{PG_ONLY}} ALTER TABLE webhook_deliveries ALTER COLUMN payload TYPE TEXT;
