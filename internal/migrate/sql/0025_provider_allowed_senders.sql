-- email_providers.allowed_domains is renamed for what it holds now.
--
-- It held bare From domains until a provider could be restricted to exact
-- addresses too; since then it holds allowed senders, each an address or
-- *@domain. The data is unchanged: rows written before then still carry bare
-- domains, which senders.Canonical reads as *@domain.
ALTER TABLE email_providers RENAME COLUMN allowed_domains TO allowed_senders;
