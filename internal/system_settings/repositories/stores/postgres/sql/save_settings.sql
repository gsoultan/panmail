INSERT INTO system_settings (id, base_url, retry_pattern, log_retention_days, webhook_retention_days,
                             message_retention_days, outbox_retention_days, app_log_retention_days,
                             inbound_retention_days, archive_retention_days, quarantine_retention_days,
                             content_redaction, updated_at)
VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (id) DO UPDATE SET base_url = EXCLUDED.base_url,
                               retry_pattern = EXCLUDED.retry_pattern,
                               log_retention_days = EXCLUDED.log_retention_days,
                               webhook_retention_days = EXCLUDED.webhook_retention_days,
                               message_retention_days = EXCLUDED.message_retention_days,
                               outbox_retention_days = EXCLUDED.outbox_retention_days,
                               app_log_retention_days = EXCLUDED.app_log_retention_days,
                               inbound_retention_days = EXCLUDED.inbound_retention_days,
                               archive_retention_days = EXCLUDED.archive_retention_days,
                               quarantine_retention_days = EXCLUDED.quarantine_retention_days,
                               content_redaction = EXCLUDED.content_redaction,
                               updated_at = EXCLUDED.updated_at
