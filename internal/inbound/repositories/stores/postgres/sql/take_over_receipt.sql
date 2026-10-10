-- Takes over a claim held by a post that died before settling it. The
-- conditions are in the UPDATE rather than read first, so two posts racing to
-- take over the same stale claim cannot both win.
UPDATE inbound_receipts
SET claim_token = $4, claimed_at = $5
WHERE tenant_id = $1 AND provider_id = $2 AND dedupe_key = $3
  AND state = 'PROCESSING' AND claimed_at < $6;
