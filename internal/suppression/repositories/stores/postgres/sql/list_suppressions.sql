SELECT id, tenant_id, email, reason, created_at FROM suppressions WHERE tenant_id = $1 ORDER BY created_at DESC;
