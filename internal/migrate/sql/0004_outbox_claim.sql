-- Lease-based claiming so two workers cannot send the same message.
ALTER TABLE outbox ADD COLUMN claim_token {{UUID}};
ALTER TABLE outbox ADD COLUMN claimed_until {{TIMESTAMP}};
CREATE INDEX IF NOT EXISTS idx_outbox_claim_token ON outbox(claim_token);
