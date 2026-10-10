-- The PostgreSQL claim, which needs row locks that SQLite does not. The
-- selection is claim_due_deliveries.sql's -- fair across subscriptions, see
-- there -- and only the locking differs.
--
-- Without FOR UPDATE SKIP LOCKED this hands the same notification to two
-- workers: at READ COMMITTED each concurrent claimer runs the inner SELECT
-- before either UPDATE commits, both see the same rows, and the second
-- overwrites the first's claim token. SQLite is safe with the plain form only
-- because it serialises writers.
--
-- The lock cannot go on the ranking itself -- PostgreSQL refuses FOR UPDATE
-- alongside a window function -- so the ranked ids are locked in an outer
-- SELECT that repeats the due condition. A row another claimer committed in
-- between fails that condition when it is rechecked under the lock; one it
-- still holds is skipped rather than waited for, so this claimer takes a
-- smaller batch instead of a duplicate one.
UPDATE webhook_deliveries
SET status = 'SENDING',
    claim_token = $1,
    claimed_until = $2,
    updated_at = $3
WHERE id IN (
    SELECT id
    FROM webhook_deliveries
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
                 OR (status = 'SENDING' AND (claimed_until IS NULL OR claimed_until < $4))
              )
        ) ranked
        WHERE turn <= $6
        ORDER BY turn, created_at
        LIMIT $5
    )
      AND next_attempt_at <= $4
      AND (
            status = 'PENDING'
         OR status = 'DEFERRED'
         OR (status = 'SENDING' AND (claimed_until IS NULL OR claimed_until < $4))
      )
    FOR UPDATE SKIP LOCKED
);
