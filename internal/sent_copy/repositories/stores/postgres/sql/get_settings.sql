SELECT mode, imap_host, imap_port, imap_use_ssl, sent_folder,
       master_username, master_password, master_separator, updated_at
FROM sent_copy_settings
WHERE tenant_id = $1
