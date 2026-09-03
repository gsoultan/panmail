INSERT INTO system_settings (id, base_url, retry_pattern, log_retention_days, webhook_retention_days,
                             message_retention_days, outbox_retention_days, app_log_retention_days,
                             inbound_retention_days, archive_retention_days, quarantine_retention_days,
                             updated_at)
VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (id) DO NOTHING
