-- Lease-based claiming, so two workers cannot deliver the same notification.
-- Calling a tenant's endpoint twice for one event is not a cosmetic problem:
-- a consumer that opens a ticket per webhook opens two.
UPDATE webhook_deliveries
SET status = 'SENDING',
    claim_token = $1,
    claimed_until = $2,
    updated_at = $3
WHERE id IN (
    SELECT id
    FROM webhook_deliveries
    WHERE next_attempt_at <= $4
      AND (
            status = 'PENDING'
         OR status = 'DEFERRED'
         -- A claim that expired without being resolved belongs to a worker
         -- that died; reclaiming it is what stops the notification stranding.
         OR (status = 'SENDING' AND (claimed_until IS NULL OR claimed_until < $4))
      )
    ORDER BY created_at ASC
    LIMIT $5
);
