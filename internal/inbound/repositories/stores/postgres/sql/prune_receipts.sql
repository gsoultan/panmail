DELETE FROM inbound_receipts
WHERE created_at < $1;
