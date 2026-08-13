-- Depth and the oldest undelivered notification. Same reasoning as the outbox:
-- the age is what distinguishes a busy queue from a stalled one, and an empty
-- queue has no oldest row rather than a sentinel one.
SELECT COUNT(*), MIN(created_at)
FROM webhook_deliveries
WHERE status = 'PENDING' OR status = 'DEFERRED' OR status = 'SENDING';
