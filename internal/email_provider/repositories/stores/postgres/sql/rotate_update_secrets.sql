-- Rewrites only the sealed columns, leaving everything else untouched so a
-- rotation cannot alter configuration as a side effect.
UPDATE email_providers SET config = $2, webhook_secret = $3 WHERE id = $1;
