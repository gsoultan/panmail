-- Depth and the oldest undelivered message.
--
-- The age is the signal that matters. A pending count of five hundred is a
-- busy minute or a dead worker and the number cannot tell you which; the
-- oldest one being forty minutes old can only mean one thing.
--
-- created_at is returned as written rather than coalesced to CURRENT_TIMESTAMP:
-- the two are different shapes, and asking the driver to turn either into a
-- time.Time fails on one of them. An empty queue has no oldest row, which the
-- caller reads as NULL.
SELECT COUNT(*), MIN(created_at)
FROM outbox
WHERE status = 'PENDING' OR status = 'DEFERRED' OR status = 'SENDING';
