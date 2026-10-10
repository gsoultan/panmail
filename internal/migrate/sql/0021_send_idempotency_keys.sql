-- Idempotency keys for SendEmail, so a client that lost the answer to a send
-- can ask again without the message going out twice.
--
-- One row per accepted send that carried a key, written in the same
-- transaction as the message's outbox row. The primary key is what makes two
-- concurrent sends with the same key queue once: the second insert conflicts
-- with the first, and the loser reads the winner's message id instead of
-- queueing its own.
--
-- Not in outbox, though the row is written beside it, because an outbox row
-- is deleted the moment the message is delivered and the key has to outlive
-- that: the case the key exists for is a retry after a timeout, by which time
-- the message may well be gone from the queue.
--
-- No foreign key to tenants. A key lives a day and carries nothing but a hash
-- and a message id; a constraint would make every recently sending tenant
-- undeletable for that day for no benefit. Expired rows are pruned by the
-- outbox worker's housekeeping.
CREATE TABLE IF NOT EXISTS send_idempotency_keys (
    tenant_id {{UUID}} NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,

    -- SHA-256, hex, of the request as received with the key itself cleared.
    -- The same key with a different request is refused rather than answered
    -- with a message the caller did not ask for.
    request_hash VARCHAR(64) NOT NULL,

    message_id {{UUID}} NOT NULL,
    created_at {{TIMESTAMP}} NOT NULL,

    PRIMARY KEY (tenant_id, idempotency_key)
);

-- The prune deletes by age across every tenant.
CREATE INDEX IF NOT EXISTS idx_send_idempotency_keys_created_at
    ON send_idempotency_keys (created_at);
