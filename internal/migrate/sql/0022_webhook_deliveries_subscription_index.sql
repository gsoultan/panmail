-- The index fair claiming and endpoint back-off need.
--
-- The webhook claim now numbers each subscription's due rows oldest first
-- (ROW_NUMBER() OVER (PARTITION BY webhook_id ORDER BY created_at, id)) so no
-- one subscription can take a whole batch, and a subscription whose endpoint
-- stops answering has every waiting row moved out in one UPDATE ... WHERE
-- webhook_id = ?. idx_webhook_deliveries_claimable leads on created_at alone,
-- which serves neither: the partition needed a sort, and the back-off a scan
-- of the whole table, finished rows kept for retention included.
--
-- Partial on the claimable statuses, like idx_webhook_deliveries_claimable,
-- so it stays the size of the live queue rather than the table.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_subscription
    ON webhook_deliveries (webhook_id, created_at)
    WHERE status IN ('PENDING', 'DEFERRED', 'SENDING');
