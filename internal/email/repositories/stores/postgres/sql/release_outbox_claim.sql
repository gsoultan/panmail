UPDATE outbox
SET claim_token = NULL,
    claimed_until = NULL
WHERE id = $1;
