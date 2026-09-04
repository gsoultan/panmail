SELECT base_url,
       retry_pattern,
       log_retention_days,
       webhook_retention_days,
       message_retention_days,
       outbox_retention_days,
       app_log_retention_days,
       inbound_retention_days,
       archive_retention_days,
       quarantine_retention_days,
       content_redaction,
       updated_at
FROM system_settings
WHERE id = 1
