SELECT state
FROM inbound_receipts
WHERE tenant_id = $1 AND provider_id = $2 AND dedupe_key = $3;
