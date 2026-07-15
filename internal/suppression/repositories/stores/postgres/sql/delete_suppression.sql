DELETE FROM suppressions WHERE tenant_id = $1 AND email = $2;
