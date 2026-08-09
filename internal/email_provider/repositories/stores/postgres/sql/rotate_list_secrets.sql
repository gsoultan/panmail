-- Every stored secret, across all tenants, for a key rotation.
--
-- Deliberately not tenant-scoped: a rotation is an operator action over the
-- whole deployment, and a key left holding one tenant's rows is a key that can
-- never be retired.
SELECT id, config, webhook_secret FROM email_providers ORDER BY id;
