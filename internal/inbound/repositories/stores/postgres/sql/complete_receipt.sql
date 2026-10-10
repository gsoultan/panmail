UPDATE inbound_receipts
SET state = 'DONE'
WHERE tenant_id = $1 AND provider_id = $2 AND dedupe_key = $3
  AND claim_token = $4 AND state = 'PROCESSING';
