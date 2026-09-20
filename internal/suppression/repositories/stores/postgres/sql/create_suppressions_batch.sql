-- The VALUES list is generated at call time: the token below is replaced with
-- ($1, $2, ...), ($6, $7, ...) and so on, five parameters per row.
--
-- ON CONFLICT DO NOTHING rather than an upsert. An import is expected to
-- overlap what is already stored, and the stored row is the older, truer
-- record of why the address was suppressed -- overwriting a bounce reason
-- with "Imported from a list" loses the diagnostic that explains it. The
-- conflict target is left bare so the statement is portable across both
-- engines this store runs on.
INSERT INTO suppressions (id, tenant_id, email, reason, created_at)
VALUES __ROW_PLACEHOLDERS__
ON CONFLICT DO NOTHING;
