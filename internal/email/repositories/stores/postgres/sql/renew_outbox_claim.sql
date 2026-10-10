-- Extends a claim's lease, only while the claim is still the caller's.
--
-- The token in the WHERE clause is the whole point. A lease that lapsed may
-- already have been claimed by another worker under a new token, and that
-- worker owns the message now: this must affect zero rows rather than take it
-- back, so the caller learns it lost the claim and does not send.
UPDATE outbox
SET claimed_until = $1
WHERE id = $2
  AND claim_token = $3
  AND status = 'SENDING';
