INSERT INTO sent_copy_mailboxes (tenant_id, address, username, password, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $5)
ON CONFLICT (tenant_id, address) DO UPDATE SET
    username = EXCLUDED.username,
    password = EXCLUDED.password,
    updated_at = EXCLUDED.updated_at
