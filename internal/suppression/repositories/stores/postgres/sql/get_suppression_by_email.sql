SELECT id, tenant_id, email, reason, created_at FROM suppressions WHERE tenant_id = $1 AND email = $2;
