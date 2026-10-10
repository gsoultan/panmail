-- Lease-based claiming, so two workers cannot deliver the same notification.
-- Calling a tenant's endpoint twice for one event is not a cosmetic problem:
-- a consumer that opens a ticket per webhook opens two.
--
-- Fair across subscriptions. The claim used to take the oldest due rows
-- outright, so a subscription that owned more than a batch's worth of them --
-- typically an endpoint that had been timing out and piling up retries -- took
-- the whole batch, and every other tenant's notifications waited behind it.
-- Each subscription's rows are now numbered oldest first, at most $6 of them
-- can be taken, and the batch is filled round-robin: every subscription's
-- oldest row, then every subscription's second, and so on. A subscription
-- alone in the queue still fills the batch up to its cap; one that is not
-- alone can no longer crowd the others out.
UPDATE webhook_deliveries
SET status = 'SENDING',
    claim_token = $1,
    claimed_until = $2,
    updated_at = $3
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               created_at,
               ROW_NUMBER() OVER (PARTITION BY webhook_id ORDER BY created_at, id) AS turn
        FROM webhook_deliveries
        WHERE next_attempt_at <= $4
          AND (
                status = 'PENDING'
             OR status = 'DEFERRED'
             -- A claim that expired without being resolved belongs to a
             -- worker that died; reclaiming it is what stops the notification
             -- stranding.
             OR (status = 'SENDING' AND (claimed_until IS NULL OR claimed_until < $4))
          )
    ) ranked
    WHERE turn <= $6
    ORDER BY turn, created_at
    LIMIT $5
);
