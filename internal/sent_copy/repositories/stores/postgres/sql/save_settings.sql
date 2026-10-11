INSERT INTO sent_copy_settings (tenant_id, mode, imap_host, imap_port, imap_use_ssl, sent_folder,
                                master_username, master_password, master_separator, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
ON CONFLICT (tenant_id) DO UPDATE SET
    mode = EXCLUDED.mode,
    imap_host = EXCLUDED.imap_host,
    imap_port = EXCLUDED.imap_port,
    imap_use_ssl = EXCLUDED.imap_use_ssl,
    sent_folder = EXCLUDED.sent_folder,
    master_username = EXCLUDED.master_username,
    master_password = EXCLUDED.master_password,
    master_separator = EXCLUDED.master_separator,
    updated_at = EXCLUDED.updated_at
