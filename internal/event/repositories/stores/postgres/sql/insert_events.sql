-- One statement per batch. The VALUES list is generated at call time: the
-- token below is replaced with ($1,...,$11),($12,...,$22),... so a batch of
-- 500 events is one round trip rather than 500.
INSERT INTO email_events
    (id, tenant_id, provider_id, provider_name, message_id, type,
     recipient, subject, timestamp, metadata, error_message)
VALUES __ROWS__
ON CONFLICT (id) DO NOTHING
