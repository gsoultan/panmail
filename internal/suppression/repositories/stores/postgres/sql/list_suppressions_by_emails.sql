-- The IN list is generated at call time: the token below is replaced with
-- $2, $3, ... so one statement covers every recipient of a message.
-- Positional parameters keep the addresses out of the SQL text on both
-- engines this store runs on.
SELECT id, tenant_id, email, reason, created_at
FROM suppressions
WHERE tenant_id = $1 AND email IN (__EMAIL_PLACEHOLDERS__);
