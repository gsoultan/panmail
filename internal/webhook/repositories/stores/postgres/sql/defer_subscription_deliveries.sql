-- Pushes a subscription's waiting notifications out to $2, after its endpoint
-- has stopped answering. Without this the next claim hands the same dead
-- endpoint a fresh set of rows, and each one holds a worker for the full
-- client timeout before failing.
--
-- Only rows nobody holds: a claimed row's outcome belongs to the worker that
-- claimed it, and it reschedules that row itself. Nothing is brought forward.
UPDATE webhook_deliveries
SET next_attempt_at = $2,
    updated_at = $3
WHERE webhook_id = $1
  AND status IN ('PENDING', 'DEFERRED')
  AND next_attempt_at < $2;
