UPDATE outbox
SET status = 'SENDING',
    claim_token = $1,
    claimed_until = $2,
    updated_at = $3
WHERE id IN (
    SELECT id
    FROM outbox
    WHERE next_retry_at <= $4
      AND (
            status = 'PENDING'
         OR status = 'DEFERRED'
         OR (status = 'SENDING' AND (claimed_until IS NULL OR claimed_until < $4))
      )
    ORDER BY created_at ASC
    LIMIT $5
);
