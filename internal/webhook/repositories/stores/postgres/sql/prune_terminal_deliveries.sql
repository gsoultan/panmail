-- Delivered notifications are removed outright once they age out; failed ones
-- are kept the same length of time because they are the record an operator
-- consults when a tenant asks why they were never told.
DELETE FROM webhook_deliveries
WHERE (status = 'DELIVERED' OR status = 'FAILED')
  AND updated_at < $1;
