SELECT address, username, password, updated_at
FROM sent_copy_mailboxes
WHERE tenant_id = $1 AND address = $2
