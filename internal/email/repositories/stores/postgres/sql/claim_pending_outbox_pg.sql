-- The PostgreSQL claim, which needs row locks that SQLite does not.
--
-- Without FOR UPDATE SKIP LOCKED this hands the same message to two workers.
-- At READ COMMITTED each concurrent claimer runs the inner SELECT before
-- either UPDATE commits, both see the same rows, and the second overwrites the
-- first's claim token — so both workers believe they own it and the recipient
-- gets the message twice.
--
-- SQLite is safe with the plain form only because it serialises writers, which
-- is an accident of that engine rather than a property of the query. Running
-- the same SQL on both is what hid this: the test that exists to prove a row is
-- never claimed twice passed on SQLite for as long as SQLite was all anyone ran.
--
-- SKIP LOCKED rather than plain FOR UPDATE: a claimer should take the next
-- available row, not wait behind another worker for one it will then find
-- already claimed.
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
    FOR UPDATE SKIP LOCKED
);
